package feeds

import (
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
