package feeds

import (
	"net"
	"net/url"
	"testing"
)

func TestRedirectsStayWithinApprovedFeedHostAndCannotDowngradeHTTPS(t *testing.T) {
	from, _ := url.Parse("https://feeds.example.com/rss")
	for _, test := range []struct {
		name string
		to   string
		want bool
	}{
		{name: "same host path", to: "https://feeds.example.com/current.xml", want: true},
		{name: "other host", to: "https://content.example.com/rss", want: false},
		{name: "different port", to: "https://feeds.example.com:8443/rss", want: false},
		{name: "HTTPS downgrade", to: "http://feeds.example.com/rss", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			to, err := url.Parse(test.to)
			if err != nil {
				t.Fatal(err)
			}
			err = validateRedirectOrigin(from, to)
			if (err == nil) != test.want {
				t.Fatalf("redirect validation error = %v, want allowed=%v", err, test.want)
			}
		})
	}
	httpURL, _ := url.Parse("http://feeds.example.com/rss")
	httpsURL, _ := url.Parse("https://feeds.example.com/rss")
	if err := validateRedirectOrigin(httpURL, httpsURL); err != nil {
		t.Fatalf("HTTP to HTTPS upgrade rejected: %v", err)
	}
}

func TestIsPublicIPRejectsSpecialPurposeRanges(t *testing.T) {
	blocked := []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "100.64.0.1", "100.127.255.254", "127.0.0.1",
		"169.254.169.254", "172.16.0.1", "172.31.255.255", "192.0.0.8", "192.0.2.10",
		"192.88.99.1", "192.168.1.1", "198.18.0.1", "198.19.255.255", "198.51.100.7",
		"203.0.113.9", "224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "64:ff9b::7f00:1", "64:ff9b::a00:1",
		"64:ff9b:1::1", "100::1", "2001::1", "2001:db8::1", "2002:7f00:1::1", "2002:c0a8:101::1",
		"fc00::1", "fd12:3456::1", "fe80::1", "fec0::1", "ff02::1", "3fff::1",
	}
	for _, raw := range blocked {
		if isPublicIP(net.ParseIP(raw)) {
			t.Errorf("isPublicIP(%s) = true, want false", raw)
		}
	}
}

func TestIsPublicIPAcceptsPublicAddresses(t *testing.T) {
	allowed := []string{
		"1.1.1.1", "8.8.8.8", "93.184.216.34", "100.63.255.255", "100.128.0.1", "172.32.0.1",
		"198.17.255.255", "198.20.0.1", "::ffff:8.8.8.8", "64:ff9b::808:808",
		"2002:808:808::1", "2606:4700:4700::1111", "2a00:1450:4001::200e",
	}
	for _, raw := range allowed {
		if !isPublicIP(net.ParseIP(raw)) {
			t.Errorf("isPublicIP(%s) = false, want true", raw)
		}
	}
}
