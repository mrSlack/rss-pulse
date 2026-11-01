package httpapi

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time                 { return c.now }
func (c *fakeClock) Advance(duration time.Duration) { c.now = c.now.Add(duration) }

func newTestClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
}

func failAttempts(t *testing.T, limiter *loginLimiter, ip, account string, count int) {
	t.Helper()
	for attempt := 0; attempt < count; attempt++ {
		if _, wait, ok := limiter.begin(ip, account); !ok {
			t.Fatalf("attempt %d rejected early (wait %v)", attempt+1, wait)
		}
	}
}

func TestLoginLimiterLocksAccountAfterTenFailuresForRollingWindow(t *testing.T) {
	clock := newTestClock()
	limiter := newLoginLimiter(clock.Now)
	for attempt := 0; attempt < accountFailureLimit; attempt++ {
		// Distinct IPs so only the account limit applies.
		if _, _, ok := limiter.begin(fmt.Sprintf("192.0.2.%d", attempt), "reader@example.com"); !ok {
			t.Fatalf("failure %d rejected", attempt+1)
		}
		clock.Advance(time.Minute)
	}
	_, wait, ok := limiter.begin("198.51.100.1", "reader@example.com")
	if ok {
		t.Fatal("11th attempt within 15 minutes was allowed")
	}
	// The first failure was 10 minutes ago, so it expires in 5 minutes.
	if wait != 5*time.Minute {
		t.Fatalf("wait = %v, want 5m", wait)
	}
	if _, _, ok := limiter.begin("198.51.100.1", "other@example.com"); !ok {
		t.Fatal("another account was locked")
	}
	clock.Advance(5 * time.Minute)
	if _, _, ok := limiter.begin("198.51.100.1", "reader@example.com"); !ok {
		t.Fatal("attempt after the oldest failure left the window was rejected")
	}
	if _, _, ok := limiter.begin("198.51.100.1", "reader@example.com"); ok {
		t.Fatal("rolling window allowed more than 10 failures in 15 minutes")
	}
}

func TestLoginLimiterSuccessClearsAccountFailures(t *testing.T) {
	clock := newTestClock()
	limiter := newLoginLimiter(clock.Now)
	failAttempts(t, limiter, "192.0.2.1", "reader@example.com", accountFailureLimit-1)
	attempt, _, ok := limiter.begin("192.0.2.1", "reader@example.com")
	if !ok {
		t.Fatal("10th attempt rejected")
	}
	limiter.succeed(attempt)
	failAttempts(t, limiter, "192.0.2.2", "reader@example.com", accountFailureLimit)
}

func TestLoginLimiterCancelWithdrawsProvisionalFailure(t *testing.T) {
	clock := newTestClock()
	limiter := newLoginLimiter(clock.Now)
	failAttempts(t, limiter, "192.0.2.1", "reader@example.com", accountFailureLimit-1)
	attempt, _, _ := limiter.begin("192.0.2.1", "reader@example.com")
	limiter.cancel(attempt)
	if _, _, ok := limiter.begin("192.0.2.1", "reader@example.com"); !ok {
		t.Fatal("cancelled attempt still counted as a failure")
	}
}

func TestLoginLimiterLimitsAttemptsPerIP(t *testing.T) {
	clock := newTestClock()
	limiter := newLoginLimiter(clock.Now)
	for attempt := 0; attempt < ipAttemptLimit; attempt++ {
		started, _, ok := limiter.begin("192.0.2.1", fmt.Sprintf("user%d@example.com", attempt))
		if !ok {
			t.Fatalf("attempt %d rejected", attempt+1)
		}
		// Successful attempts count against the IP limit too.
		limiter.succeed(started)
		clock.Advance(time.Second)
	}
	_, wait, ok := limiter.begin("192.0.2.1", "fresh@example.com")
	if ok {
		t.Fatal("31st attempt from one IP within a minute was allowed")
	}
	if wait != 30*time.Second {
		t.Fatalf("wait = %v, want 30s", wait)
	}
	if _, _, ok := limiter.begin("192.0.2.2", "fresh@example.com"); !ok {
		t.Fatal("another IP was limited")
	}
	clock.Advance(30 * time.Second)
	if _, _, ok := limiter.begin("192.0.2.1", "fresh@example.com"); !ok {
		t.Fatal("IP still limited after the window")
	}
}

func TestLoginLimiterEvictsStaleEntries(t *testing.T) {
	clock := newTestClock()
	limiter := newLoginLimiter(clock.Now)
	for index := 0; index < 100; index++ {
		limiter.begin(fmt.Sprintf("192.0.2.%d", index), fmt.Sprintf("user%d@example.com", index))
	}
	clock.Advance(accountFailureWindow + time.Second)
	limiter.begin("198.51.100.1", "new@example.com")
	if ips, accounts := limiter.size(); ips != 1 || accounts != 1 {
		t.Fatalf("after sweep ips=%d accounts=%d, want 1 and 1", ips, accounts)
	}
}

func TestLoginLimiterCapsTrackedKeys(t *testing.T) {
	limiter := newLoginLimiter(newTestClock().Now)
	for index := 0; index < limiterMaxKeys+50; index++ {
		limiter.begin(fmt.Sprintf("ip-%d", index), fmt.Sprintf("user%d@example.com", index))
	}
	if ips, accounts := limiter.size(); ips > limiterMaxKeys || accounts > limiterMaxKeys {
		t.Fatalf("ips=%d accounts=%d exceed cap %d", ips, accounts, limiterMaxKeys)
	}
}

func TestClientIPTrustsRealIPOnlyFromTrustedProxies(t *testing.T) {
	trusted, err := ParseTrustedProxyCIDRs("")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, remote, realIP, want string
	}{
		{"direct client", "203.0.113.7:5000", "", "203.0.113.7"},
		{"untrusted peer header ignored", "203.0.113.7:5000", "198.51.100.9", "203.0.113.7"},
		{"docker proxy header used", "172.18.0.5:40000", "198.51.100.9", "198.51.100.9"},
		{"loopback proxy header used", "127.0.0.1:40000", "198.51.100.9", "198.51.100.9"},
		{"ipv6 ula proxy header used", "[fd00::5]:40000", "2001:db8::1", "2001:db8::1"},
		{"trusted proxy without header", "10.0.0.2:40000", "", "10.0.0.2"},
		{"trusted proxy with garbage header", "10.0.0.2:40000", "not-an-ip", "10.0.0.2"},
		{"ipv4-mapped peer", "[::ffff:203.0.113.7]:5000", "198.51.100.9", "203.0.113.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/api/auth/login", nil)
			request.RemoteAddr = tc.remote
			if tc.realIP != "" {
				request.Header.Set("X-Real-IP", tc.realIP)
			}
			if got := clientIP(request, trusted); got != tc.want {
				t.Fatalf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
	none, err := ParseTrustedProxyCIDRs("none")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/api/auth/login", nil)
	request.RemoteAddr = "172.18.0.5:40000"
	request.Header.Set("X-Real-IP", "198.51.100.9")
	if got := clientIP(request, none); got != "172.18.0.5" {
		t.Fatalf("with no trusted proxies clientIP = %q", got)
	}
}

func TestParseTrustedProxyCIDRsRejectsInvalidValues(t *testing.T) {
	for _, raw := range []string{"10.0.0.0/33", "not-a-cidr", "10.0.0.1", ",,"} {
		if _, err := ParseTrustedProxyCIDRs(raw); err == nil {
			t.Fatalf("ParseTrustedProxyCIDRs(%q) succeeded", raw)
		}
	}
	prefixes, err := ParseTrustedProxyCIDRs(" 10.1.2.3/8 , fd00::/8 ")
	if err != nil || len(prefixes) != 2 || prefixes[0].String() != "10.0.0.0/8" {
		t.Fatalf("prefixes = %v, err = %v", prefixes, err)
	}
}

func TestRetryAfterSecondsRoundsUp(t *testing.T) {
	for wait, want := range map[time.Duration]string{time.Millisecond: "1", 0: "1", 1500 * time.Millisecond: "2", 5 * time.Minute: "300"} {
		if got := retryAfterSeconds(wait); got != want {
			t.Fatalf("retryAfterSeconds(%v) = %q, want %q", wait, got, want)
		}
	}
}
