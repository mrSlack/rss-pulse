package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"
)

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
