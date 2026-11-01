package httpapi

import (
	"cmp"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

// Sign-in throttling limits. The limiter keeps its state in process memory,
// which is correct for the single API instance Pulse runs. Several API
// replicas would each enforce these limits separately.
const (
	accountFailureLimit  = 10
	accountFailureWindow = 15 * time.Minute
	ipAttemptLimit       = 30
	ipAttemptWindow      = time.Minute
	// limiterMaxKeys bounds memory per map. Each key holds at most
	// accountFailureLimit or ipAttemptLimit timestamps.
	limiterMaxKeys       = 50_000
	limiterSweepInterval = time.Minute
	// limiterFullRetry is how long a full map that could not be compacted
	// rejects new keys without scanning it again, so a saturated map does
	// not cost a full scan on every request.
	limiterFullRetry = time.Second
	// limiterFullRetryAfter is the wait reported to a client rejected because
	// a map is full of limited keys.
	limiterFullRetryAfter = time.Minute
)

// DefaultTrustedProxyCIDRs covers loopback, RFC 1918 and IPv6 unique local
// addresses, which includes the Docker networks between Nginx and the API.
const DefaultTrustedProxyCIDRs = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7"

// ParseTrustedProxyCIDRs parses the comma-separated TRUSTED_PROXY_CIDRS value.
// An empty value selects DefaultTrustedProxyCIDRs; "none" trusts no proxy, so
// X-Real-IP is always ignored.
func ParseTrustedProxyCIDRs(raw string) ([]netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultTrustedProxyCIDRs
	}
	if strings.EqualFold(raw, "none") {
		return []netip.Prefix{}, nil
	}
	var prefixes []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", part, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("no trusted proxy CIDRs in %q", raw)
	}
	return prefixes, nil
}

// clientIP returns the direct peer address, or the X-Real-IP header when the
// peer is a trusted proxy (Nginx sets X-Real-IP to the address it received the
// connection from). Headers from untrusted peers are ignored because any
// client can send them.
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	peer, ok := parseAddr(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	for _, prefix := range trusted {
		if prefix.Contains(peer) {
			if forwarded, ok := parseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ok {
				return forwarded.String()
			}
			break
		}
	}
	return peer.String()
}

func parseAddr(value string) (netip.Addr, bool) {
	if value == "" {
		return netip.Addr{}, false
	}
	if addrPort, err := netip.ParseAddrPort(value); err == nil {
		return addrPort.Addr().Unmap(), true
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// loginLimiter throttles sign-in attempts per client IP (all attempts) and
// per account (failed attempts). A started attempt is provisionally counted
// as a failure so concurrent guesses cannot all pass the check before any of
// them is recorded; success clears the account and cancel withdraws the
// provisional entry for outcomes that are not credential failures.
type loginLimiter struct {
	mu        sync.Mutex
	now       func() time.Time
	accounts  map[string][]time.Time
	ips       map[string][]time.Time
	lastSweep time.Time
	maxKeys   int
	// ipsFullUntil and accountsFullUntil are set when compaction could not
	// free a slot; until then new keys are rejected without another scan.
	ipsFullUntil      time.Time
	accountsFullUntil time.Time
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	if now == nil {
		now = time.Now
	}
	return &loginLimiter{now: now, maxKeys: limiterMaxKeys, accounts: map[string][]time.Time{}, ips: map[string][]time.Time{}}
}

// loginAttempt identifies a provisional failure recorded by begin.
type loginAttempt struct {
	account string
	at      time.Time
}

// begin checks both limits. When the attempt is allowed it is recorded and
// returned; otherwise begin returns how long the client must wait.
func (l *loginLimiter) begin(ip, account string) (loginAttempt, time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweepLocked(now)
	ipTimes := prune(l.ips[ip], now.Add(-ipAttemptWindow))
	accountTimes := prune(l.accounts[account], now.Add(-accountFailureWindow))
	var wait time.Duration
	if len(ipTimes) >= ipAttemptLimit {
		wait = max(wait, ipTimes[len(ipTimes)-ipAttemptLimit].Add(ipAttemptWindow).Sub(now))
	}
	if len(accountTimes) >= accountFailureLimit {
		wait = max(wait, accountTimes[len(accountTimes)-accountFailureLimit].Add(accountFailureWindow).Sub(now))
	}
	storeTimes(l.ips, ip, ipTimes)
	storeTimes(l.accounts, account, accountTimes)
	if wait > 0 {
		return loginAttempt{}, wait, false
	}
	// A key without live timestamps is absent now and needs a free slot.
	if len(ipTimes) == 0 && !l.makeRoomLocked(l.ips, &l.ipsFullUntil, ipAttemptWindow, ipAttemptLimit, now) ||
		len(accountTimes) == 0 && !l.makeRoomLocked(l.accounts, &l.accountsFullUntil, accountFailureWindow, accountFailureLimit, now) {
		return loginAttempt{}, limiterFullRetryAfter, false
	}
	l.ips[ip] = append(ipTimes, now)
	l.accounts[account] = append(accountTimes, now)
	return loginAttempt{account: account, at: now}, 0, true
}

// succeed clears the account's failures after a correct password.
func (l *loginLimiter) succeed(attempt loginAttempt) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.accounts, attempt.account)
}

// cancel withdraws the provisional failure, for example after a server error.
func (l *loginLimiter) cancel(attempt loginAttempt) {
	l.mu.Lock()
	defer l.mu.Unlock()
	times := l.accounts[attempt.account]
	for index, at := range times {
		if at.Equal(attempt.at) {
			times = append(times[:index], times[index+1:]...)
			break
		}
	}
	storeTimes(l.accounts, attempt.account, times)
}

func (l *loginLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < limiterSweepInterval {
		return
	}
	l.lastSweep = now
	for key, times := range l.ips {
		storeTimes(l.ips, key, prune(times, now.Add(-ipAttemptWindow)))
	}
	for key, times := range l.accounts {
		storeTimes(l.accounts, key, prune(times, now.Add(-accountFailureWindow)))
	}
}

// makeRoomLocked reports whether entries can take one more key without
// exceeding maxKeys, so a flood of distinct e-mail addresses or IPs cannot
// grow memory without bound. Eviction must not let that flood erase active
// limits: when the map is full, expired timestamps are pruned first, then
// entries are evicted with the fewest live timestamps first and, among
// equals, the oldest last activity first. Entries at their limit (locked
// accounts or IPs) are never evicted. Compaction frees 10% of the map so the
// O(n log n) scan runs at most once per maxKeys/10 new keys.
//
// If every remaining entry is locked, the new key is rejected (fail closed)
// instead of admitted untracked or admitted by evicting a lock: an untracked
// key could be guessed without limit, and evicting a lock is the bypass this
// guards against. Filling the account map with locks takes 500,000 failed
// attempts within 15 minutes from over 1,000 IPs; the rejection lasts only
// until locks expire or are cleared by a successful sign-in.
func (l *loginLimiter) makeRoomLocked(entries map[string][]time.Time, fullUntil *time.Time, window time.Duration, limit int, now time.Time) bool {
	if len(entries) < l.maxKeys {
		return true
	}
	if now.Before(*fullUntil) {
		return false
	}
	type candidate struct {
		key   string
		count int
		last  time.Time
	}
	cutoff := now.Add(-window)
	var candidates []candidate
	for key, times := range entries {
		times = prune(times, cutoff)
		storeTimes(entries, key, times)
		if len(times) > 0 && len(times) < limit {
			candidates = append(candidates, candidate{key: key, count: len(times), last: times[len(times)-1]})
		}
	}
	target := l.maxKeys - max(1, l.maxKeys/10)
	if excess := len(entries) - target; excess > 0 {
		slices.SortFunc(candidates, func(a, b candidate) int {
			if order := cmp.Compare(a.count, b.count); order != 0 {
				return order
			}
			return a.last.Compare(b.last)
		})
		for _, evicted := range candidates[:min(excess, len(candidates))] {
			delete(entries, evicted.key)
		}
	}
	if len(entries) < l.maxKeys {
		return true
	}
	*fullUntil = now.Add(limiterFullRetry)
	return false
}

func (l *loginLimiter) size() (ips, accounts int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.ips), len(l.accounts)
}

// prune drops timestamps not after cutoff. Timestamps are appended in order.
func prune(times []time.Time, cutoff time.Time) []time.Time {
	index := 0
	for index < len(times) && !times[index].After(cutoff) {
		index++
	}
	return times[index:]
}

func storeTimes(entries map[string][]time.Time, key string, times []time.Time) {
	if len(times) == 0 {
		delete(entries, key)
		return
	}
	entries[key] = times
}

func retryAfterSeconds(wait time.Duration) string {
	return fmt.Sprint(max(1, int(math.Ceil(wait.Seconds()))))
}
