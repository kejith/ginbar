package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type readinessCheckerFunc func(context.Context) error

func (f readinessCheckerFunc) Ping(ctx context.Context) error { return f(ctx) }

func newReadinessTestHandler(checker ReadinessChecker, timeout time.Duration) http.Handler {
	cfg := DefaultConfig()
	cfg.Readiness = checker
	cfg.ReadinessTimeout = timeout
	return NewWithConfig(&apiStore{}, cfg).Handler()
}

func TestHealthIsLivenessOnly(t *testing.T) {
	var calls atomic.Int32
	handler := newReadinessTestHandler(readinessCheckerFunc(func(context.Context) error {
		calls.Add(1)
		return errors.New("database unavailable")
	}), 20*time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("healthz touched readiness dependency %d times", calls.Load())
	}
	var body map[string]string
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body=%v", body)
	}
}

func TestReadinessUnavailableRecoversWithoutDisclosingDependencyError(t *testing.T) {
	const secret = "postgres://operator:super-secret@db.internal/ginbar"
	var ready atomic.Bool
	handler := newReadinessTestHandler(readinessCheckerFunc(func(context.Context) error {
		if !ready.Load() {
			return errors.New(secret)
		}
		return nil
	}), 20*time.Millisecond)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status=%d body=%s", first.Code, first.Body.String())
	}
	if got := first.Body.String(); got != "{\"status\":\"not_ready\"}\n" || strings.Contains(got, "super-secret") || strings.Contains(got, "db.internal") {
		t.Fatalf("unsafe unavailable body=%q", got)
	}
	if first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", first.Header().Get("Cache-Control"))
	}

	ready.Store(true)
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if second.Code != http.StatusOK || second.Body.String() != "{\"status\":\"ready\"}\n" {
		t.Fatalf("recovered status=%d body=%s", second.Code, second.Body.String())
	}
}

func TestReadinessWithoutConfiguredDependencyFailsClosed(t *testing.T) {
	handler := New(&apiStore{}).Handler()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if res.Code != http.StatusServiceUnavailable || res.Body.String() != "{\"status\":\"not_ready\"}\n" {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestReadinessTimeoutBoundsDependencyWait(t *testing.T) {
	const timeout = 5 * time.Millisecond
	var sawDeadline atomic.Bool
	handler := newReadinessTestHandler(readinessCheckerFunc(func(ctx context.Context) error {
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= 50*time.Millisecond {
			sawDeadline.Store(true)
		}
		<-ctx.Done()
		return ctx.Err()
	}), timeout)

	started := time.Now()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	elapsed := time.Since(started)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if !sawDeadline.Load() {
		t.Fatal("readiness checker did not receive bounded context deadline")
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("readiness timeout was not bounded: %s", elapsed)
	}
}

func TestReadinessAllowsOnlyOneDependencyProbeAtATime(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var active atomic.Int32
	var maxActive atomic.Int32
	handler := newReadinessTestHandler(readinessCheckerFunc(func(context.Context) error {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			seen := maxActive.Load()
			if current <= seen || maxActive.CompareAndSwap(seen, current) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		return nil
	}), time.Second)

	first := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first readiness probe did not enter checker")
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("overlap status=%d body=%s", second.Code, second.Body.String())
	}
	if maxActive.Load() != 1 {
		t.Fatalf("max active dependency probes=%d", maxActive.Load())
	}

	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first readiness probe did not finish")
	}
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
}
