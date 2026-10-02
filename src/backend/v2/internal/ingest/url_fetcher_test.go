package ingest

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

type fakeResolver struct {
	addresses []netip.Addr
	err       error
}

func (r fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r.addresses, r.err
}

type recordingDialer struct {
	calls int
}

func (d *recordingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls++
	return nil, errors.New("dial stopped by test")
}

func TestValidateRemoteURL(t *testing.T) {
	for _, raw := range []string{
		"ftp://example.com/a",
		"https://user:pass@example.com/a",
		"https://example.com/a#fragment",
		"https:///missing-host",
		"http://example.com:0/a",
		"http://127.0.0.1/a",
		"http://[::1]/a",
	} {
		if _, err := validateRemoteURL(raw); !errors.Is(err, ErrUnsafeURL) {
			t.Fatalf("validateRemoteURL(%q) error = %v", raw, err)
		}
	}
	if _, err := validateRemoteURL("https://example.com:8443/a"); err != nil {
		t.Fatal(err)
	}
}

func TestAllowedRemoteAddrRejectsInternalAndSpecialRanges(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.169.254",
		"100.64.0.1",
		"198.18.0.1",
		"::1",
		"fc00::1",
		"fe80::1",
		"2001:db8::1",
	} {
		if allowedRemoteAddr(netip.MustParseAddr(raw)) {
			t.Fatalf("address %s unexpectedly allowed", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !allowedRemoteAddr(netip.MustParseAddr(raw)) {
			t.Fatalf("address %s unexpectedly blocked", raw)
		}
	}
}

func TestDialContextRejectsPrivateDNSAnswersWithoutDialing(t *testing.T) {
	dialer := &recordingDialer{}
	fetcher := &HTTPURLFetcher{
		resolver: fakeResolver{addresses: []netip.Addr{netip.MustParseAddr("10.1.2.3")}},
		dialer:   dialer,
	}
	_, err := fetcher.dialContext(context.Background(), "tcp", "example.test:443")
	if !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("error = %v", err)
	}
	if dialer.calls != 0 {
		t.Fatalf("dialer called %d times", dialer.calls)
	}
}

func TestURLFetcherConfigRequiresExplicitBounds(t *testing.T) {
	_, err := NewHTTPURLFetcher(URLFetcherConfig{})
	if err == nil {
		t.Fatal("empty URL fetcher config unexpectedly accepted")
	}
	_, err = NewHTTPURLFetcher(URLFetcherConfig{
		MaxBytes:              1024,
		ConnectTimeout:        time.Second,
		ResponseHeaderTimeout: time.Second,
		MaxRedirects:          3,
	})
	if err != nil {
		t.Fatal(err)
	}
}
