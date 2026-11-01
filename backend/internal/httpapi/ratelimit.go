package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/pulse-reader/app/internal/store"
)

// Sign-in throttling limits. The state lives in PostgreSQL (login_attempts), so
// it survives restarts and is shared by every API instance.
const (
	accountFailureLimit  = 10
	accountFailureWindow = 15 * time.Minute
	ipAttemptLimit       = 30
	ipAttemptWindow      = time.Minute
	// limiterSweepInterval is how often each API instance deletes attempts
	// that have left their window.
	limiterSweepInterval = time.Minute
)

var loginThrottle = store.LoginThrottle{
	AccountFailureLimit:  accountFailureLimit,
	AccountFailureWindow: accountFailureWindow,
	IPAttemptLimit:       ipAttemptLimit,
	IPAttemptWindow:      ipAttemptWindow,
}

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
// provisional entry for outcomes that are not credential failures. The store
// performs the check and the recording atomically across API instances.
type loginLimiter struct {
	store  *store.Store
	now    func() time.Time
	logger *slog.Logger

	mu        sync.Mutex
	lastSweep time.Time
}

func newLoginLimiter(repository *store.Store, now func() time.Time, logger *slog.Logger) *loginLimiter {
	if now == nil {
		now = time.Now
	}
	return &loginLimiter{store: repository, now: now, logger: logger}
}

// begin checks both limits. When the attempt is allowed it is recorded and
// returned with a zero wait; otherwise begin returns how long the client must
// wait. An error means the limits could not be checked, and the caller must
// refuse the sign-in (fail closed).
func (l *loginLimiter) begin(ctx context.Context, ip, account string) (store.LoginAttempt, time.Duration, error) {
	now := l.now()
	l.sweep(ctx, now)
	return l.store.BeginLoginAttempt(ctx, loginThrottle, ip, account, now)
}

// succeed clears the account's failures after a correct password.
func (l *loginLimiter) succeed(ctx context.Context, attempt store.LoginAttempt) {
	if err := l.store.ClearLoginFailures(ctx, attempt); err != nil {
		l.logger.Warn("sign-in rate limit: clearing account failures failed", slog.String("error", err.Error()))
	}
}

// cancel withdraws the provisional failure, for example after a server error.
func (l *loginLimiter) cancel(ctx context.Context, attempt store.LoginAttempt) {
	if err := l.store.CancelLoginAttempt(ctx, attempt); err != nil {
		l.logger.Warn("sign-in rate limit: withdrawing attempt failed", slog.String("error", err.Error()))
	}
}

// sweep deletes expired attempts at most once per limiterSweepInterval on
// this instance. Rows therefore live at most accountFailureWindow plus one
// sweep interval, and every row belongs to an attempt the per-IP limit allowed.
func (l *loginLimiter) sweep(ctx context.Context, now time.Time) {
	l.mu.Lock()
	if now.Sub(l.lastSweep) < limiterSweepInterval {
		l.mu.Unlock()
		return
	}
	l.lastSweep = now
	l.mu.Unlock()
	if _, err := l.store.DeleteExpiredLoginAttempts(ctx, loginThrottle, now); err != nil {
		l.logger.Warn("sign-in rate limit: deleting expired attempts failed", slog.String("error", err.Error()))
	}
}

func retryAfterSeconds(wait time.Duration) string {
	return fmt.Sprint(max(1, int(math.Ceil(wait.Seconds()))))
}
