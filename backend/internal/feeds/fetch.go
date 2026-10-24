package feeds

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxFeedBytes = 5 << 20

var ErrUnsafeURL = errors.New("the address points to a local or unavailable network")
var feedHTTPClient = newSafeHTTPClient()

func Fetch(ctx context.Context, rawURL string) (Document, error) {
	feedURL, err := validateURL(ctx, strings.TrimSpace(rawURL))
	if err != nil {
		return Document{}, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL.String(), nil)
	if err != nil {
		return Document{}, errors.New("could not prepare the RSS feed request")
	}
	request.Header.Set("Accept", "application/atom+xml, application/rss+xml, application/xml, text/xml, */*;q=0.8")
	request.Header.Set("User-Agent", "PulseReader/0.1 (+self-hosted RSS reader)")

	response, err := feedHTTPClient.Do(request)
	if err != nil {
		return Document{}, fmt.Errorf("could not fetch the RSS feed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Document{}, fmt.Errorf("the source returned HTTP %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxFeedBytes+1))
	if err != nil {
		return Document{}, errors.New("could not read the RSS source response")
	}
	if len(body) > maxFeedBytes {
		return Document{}, errors.New("the RSS feed exceeds the 5 MB size limit")
	}

	baseURL := feedURL
	if response.Request != nil && response.Request.URL != nil {
		baseURL = response.Request.URL
	}
	document, err := Parse(body, baseURL)
	if err != nil {
		return Document{}, err
	}
	if document.Title == "" {
		document.Title = feedURL.Hostname()
	}
	if document.SiteURL == "" {
		document.SiteURL = feedURL.Scheme + "://" + feedURL.Host
	}
	return document, nil
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
			_, err := validateURL(request.Context(), request.URL.String())
			return err
		},
	}
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

func isPublicIP(ip net.IP) bool {
	if ipv4 := ip.To4(); ipv4 != nil {
		ip = ipv4
	}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsUnspecified()
}
