package httpapi

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
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
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	if now == nil {
		now = time.Now
	}
	return &loginLimiter{now: now, accounts: map[string][]time.Time{}, ips: map[string][]time.Time{}}
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
	if wait > 0 {
		storeTimes(l.ips, ip, ipTimes)
		storeTimes(l.accounts, account, accountTimes)
		return loginAttempt{}, wait, false
	}
	l.ips[ip] = append(ipTimes, now)
	l.accounts[account] = append(accountTimes, now)
	l.capLocked()
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

// capLocked evicts arbitrary entries when a map exceeds limiterMaxKeys, so a
// flood of distinct e-mail addresses or IPs cannot grow memory without bound.
func (l *loginLimiter) capLocked() {
	for _, entries := range []map[string][]time.Time{l.ips, l.accounts} {
		for key := range entries {
			if len(entries) <= limiterMaxKeys {
				break
			}
			delete(entries, key)
		}
	}
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
