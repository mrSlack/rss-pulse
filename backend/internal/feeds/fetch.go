package feeds

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const maxFeedBytes = 5 << 20

var ErrUnsafeURL = errors.New("the address points to a local or unavailable network")

const maxValidatorBytes = 1024

// Validators are the HTTP cache validators of a previous successful response.
// Empty fields are not sent.
type Validators struct {
	ETag         string
	LastModified string
}

// Result is the outcome of a fetch. When NotModified is true the source
// answered 304 and Document is empty. Validators are the ones to store for the
// next conditional request.
type Result struct {
	Document    Document
	NotModified bool
	Validators  Validators
}

// fetcher holds the HTTP client and URL validation used for feed requests.
// Tests construct their own fetcher to reach httptest servers; production code
// always uses defaultFetcher, whose client dials public addresses only.
type fetcher struct {
	client   *http.Client
	validate func(context.Context, string) (*url.URL, error)
}

var defaultFetcher = fetcher{client: newSafeHTTPClient(), validate: validateURL}

// Fetch downloads and parses a feed. Non-empty validators are sent as
// If-None-Match / If-Modified-Since; a 304 response yields a NotModified result.
func Fetch(ctx context.Context, rawURL string, validators Validators) (Result, error) {
	return defaultFetcher.fetch(ctx, rawURL, validators)
}

func (f fetcher) fetch(ctx context.Context, rawURL string, validators Validators) (Result, error) {
	feedURL, err := f.validate(ctx, strings.TrimSpace(rawURL))
	if err != nil {
		return Result{}, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL.String(), nil)
	if err != nil {
		return Result{}, errors.New("could not prepare the RSS feed request")
	}
	request.Header.Set("Accept", "application/atom+xml, application/rss+xml, application/xml, text/xml, */*;q=0.8")
	request.Header.Set("User-Agent", "PulseReader/0.1 (+self-hosted RSS reader)")
	conditional := validators.ETag != "" || validators.LastModified != ""
	if validators.ETag != "" {
		request.Header.Set("If-None-Match", validators.ETag)
	}
	if validators.LastModified != "" {
		request.Header.Set("If-Modified-Since", validators.LastModified)
	}

	response, err := f.client.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("could not fetch the RSS feed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified && conditional {
		return Result{NotModified: true, Validators: responseValidators(response, validators)}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, fmt.Errorf("the source returned HTTP %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxFeedBytes+1))
	if err != nil {
		return Result{}, errors.New("could not read the RSS source response")
	}
	if len(body) > maxFeedBytes {
		return Result{}, errors.New("the RSS feed exceeds the 5 MB size limit")
	}

	baseURL := feedURL
	if response.Request != nil && response.Request.URL != nil {
		baseURL = response.Request.URL
	}
	document, err := Parse(body, baseURL)
	if err != nil {
		return Result{}, err
	}
	if document.Title == "" {
		document.Title = feedURL.Hostname()
	}
	if document.SiteURL == "" {
		document.SiteURL = feedURL.Scheme + "://" + feedURL.Host
	}
	return Result{Document: document, Validators: responseValidators(response, Validators{})}, nil
}

// responseValidators reads ETag and Last-Modified from a response, keeping a
// fallback value for each header that is absent or unreasonably long.
func responseValidators(response *http.Response, fallback Validators) Validators {
	result := fallback
	if value := strings.TrimSpace(response.Header.Get("ETag")); value != "" && len(value) <= maxValidatorBytes {
		result.ETag = value
	}
	if value := strings.TrimSpace(response.Header.Get("Last-Modified")); value != "" && len(value) <= maxValidatorBytes {
		result.LastModified = value
	}
	return result
}

func newSafeHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialPublic,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 7 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   12 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if err := validateRedirectOrigin(via[0].URL, request.URL); err != nil {
				return err
			}
			_, err := validateURL(request.Context(), request.URL.String())
			return err
		},
	}
}

func validateRedirectOrigin(from, to *url.URL) error {
	if from == nil || to == nil || !strings.EqualFold(from.Hostname(), to.Hostname()) {
		return errors.New("redirect target is outside the approved feed host")
	}
	if from.Scheme == "https" && to.Scheme != "https" {
		return errors.New("redirect target cannot downgrade HTTPS")
	}
	fromPort, toPort := effectivePort(from), effectivePort(to)
	secureUpgrade := from.Scheme == "http" && to.Scheme == "https" && fromPort == "80" && toPort == "443"
	if fromPort != toPort && !secureUpgrade {
		return errors.New("redirect target is outside the approved feed port")
	}
	return nil
}

func effectivePort(parsed *url.URL) string {
	if port := parsed.Port(); port != "" {
		return port
	}
	if parsed.Scheme == "https" {
		return "443"
	}
	return "80"
}

func validateURL(ctx context.Context, raw string) (*url.URL, error) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("enter a complete RSS feed URL without a username or password")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("only HTTP and HTTPS URLs are supported")
	}
	if _, err := lookupPublicIPs(ctx, parsed.Hostname()); err != nil {
		return nil, err
	}
	return parsed, nil
}

func dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := lookupPublicIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, ip := range ips {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = ErrUnsafeURL
	}
	return nil, lastErr
}

func lookupPublicIPs(ctx context.Context, host string) ([]net.IP, error) {
	var ips []net.IP
	if literal := net.ParseIP(host); literal != nil {
		ips = []net.IP{literal}
	} else {
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, errors.New("could not resolve the RSS source host")
		}
		for _, address := range addresses {
			ips = append(ips, address.IP)
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("the RSS source has no IP addresses")
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return nil, ErrUnsafeURL
		}
	}
	return ips, nil
}

// blockedPrefixes lists special-purpose ranges (IANA IPv4/IPv6 special-purpose
// registries) that must never be reachable from a feed fetch, in addition to
// the private, loopback, link-local, and multicast checks below.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation (TEST-NET-1)
	netip.MustParsePrefix("192.88.99.0/24"),  // deprecated 6to4 relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation (TEST-NET-2)
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation (TEST-NET-3)
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved and limited broadcast
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("100::/64"),        // discard-only
	netip.MustParsePrefix("2001::/23"),       // IETF protocol assignments, incl. Teredo
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("3fff::/20"),       // documentation
	netip.MustParsePrefix("fec0::/10"),       // deprecated site-local
}

var (
	globalUnicastIPv6 = netip.MustParsePrefix("2000::/3")
	nat64Prefix       = netip.MustParsePrefix("64:ff9b::/96")
	sixToFourPrefix   = netip.MustParsePrefix("2002::/16")
)

func isPublicIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	return isPublicAddr(address.Unmap())
}

func isPublicAddr(address netip.Addr) bool {
	if !address.IsValid() || address.Zone() != "" {
		return false
	}
	// Translation prefixes embed an IPv4 address; the embedded address decides.
	if nat64Prefix.Contains(address) {
		bytes := address.As16()
		return isPublicAddr(netip.AddrFrom4([4]byte(bytes[12:16])))
	}
	if sixToFourPrefix.Contains(address) {
		bytes := address.As16()
		return isPublicAddr(netip.AddrFrom4([4]byte(bytes[2:6])))
	}
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	if address.Is6() && !globalUnicastIPv6.Contains(address) {
		return false
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
