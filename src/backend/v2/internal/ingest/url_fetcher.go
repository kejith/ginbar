package ingest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"
)

type netResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type contextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type URLFetcherConfig struct {
	MaxBytes              int64
	ConnectTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
	MaxRedirects          int
}

type HTTPURLFetcher struct {
	maxBytes     int64
	maxRedirects int
	resolver     netResolver
	dialer       contextDialer
	client       *http.Client
}

func NewHTTPURLFetcher(cfg URLFetcherConfig) (*HTTPURLFetcher, error) {
	if cfg.MaxBytes <= 0 {
		return nil, errors.New("URL fetch max bytes must be positive")
	}
	if cfg.ConnectTimeout <= 0 || cfg.ResponseHeaderTimeout <= 0 {
		return nil, errors.New("URL fetch timeouts must be positive")
	}
	if cfg.MaxRedirects < 0 {
		return nil, errors.New("URL fetch MaxRedirects cannot be negative")
	}

	fetcher := &HTTPURLFetcher{
		maxBytes:     cfg.MaxBytes,
		maxRedirects: cfg.MaxRedirects,
		resolver:     net.DefaultResolver,
		dialer:       &net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           fetcher.dialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   cfg.ConnectTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
	}
	fetcher.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > fetcher.maxRedirects {
				return errors.New("too many URL redirects")
			}
			if _, err := validateRemoteURL(request.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
	return fetcher, nil
}

func (f *HTTPURLFetcher) Fetch(ctx context.Context, rawURL string) (FetchedSource, error) {
	parsed, err := validateRemoteURL(rawURL)
	if err != nil {
		return FetchedSource{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return FetchedSource{}, fmt.Errorf("create URL source request: %w", err)
	}
	request.Header.Set("Accept", "image/*,video/*,application/octet-stream;q=0.5,*/*;q=0.1")

	response, err := f.client.Do(request)
	if err != nil {
		return FetchedSource{}, fmt.Errorf("fetch media source URL: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return FetchedSource{}, fmt.Errorf("fetch media source URL: unexpected HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > f.maxBytes {
		response.Body.Close()
		return FetchedSource{}, ErrSourceTooLarge
	}
	effective, err := validateRemoteURL(response.Request.URL.String())
	if err != nil {
		response.Body.Close()
		return FetchedSource{}, err
	}
	declaredMIME := response.Header.Get("Content-Type")
	if len(declaredMIME) > maxDeclaredMIMEBytes {
		declaredMIME = declaredMIME[:maxDeclaredMIMEBytes]
	}
	return FetchedSource{
		Body:         response.Body,
		EffectiveURL: effective.String(),
		DeclaredMIME: declaredMIME,
	}, nil
}

func (f *HTTPURLFetcher) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse URL source address: %w", err)
	}

	if literal, err := netip.ParseAddr(host); err == nil {
		literal = literal.Unmap()
		if !allowedRemoteAddr(literal) {
			return nil, ErrUnsafeURL
		}
		return f.dialer.DialContext(ctx, network, net.JoinHostPort(literal.String(), port))
	}

	addresses, err := f.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve media source host: %w", err)
	}
	var lastDialErr error
	foundAllowed := false
	for _, address := range addresses {
		candidate := address.Unmap()
		if !allowedRemoteAddr(candidate) {
			continue
		}
		foundAllowed = true
		connection, dialErr := f.dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastDialErr = dialErr
	}
	if !foundAllowed {
		return nil, ErrUnsafeURL
	}
	if lastDialErr != nil {
		return nil, fmt.Errorf("connect media source host: %w", lastDialErr)
	}
	return nil, errors.New("media source host resolved without usable addresses")
}

func validateRemoteURL(rawURL string) (*url.URL, error) {
	if len(rawURL) == 0 || len(rawURL) > maxSourceURLBytes {
		return nil, ErrUnsafeURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid URL", ErrUnsafeURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: only http and https are allowed", ErrUnsafeURL)
	}
	if parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, ErrUnsafeURL
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 {
			return nil, ErrUnsafeURL
		}
	}
	if literal, err := netip.ParseAddr(parsed.Hostname()); err == nil && !allowedRemoteAddr(literal) {
		return nil, ErrUnsafeURL
	}
	return parsed, nil
}

func allowedRemoteAddr(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, blocked := range blockedRemotePrefixes {
		if blocked.Contains(address) {
			return false
		}
	}
	return true
}

var blockedRemotePrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fec0::/10"),
}
