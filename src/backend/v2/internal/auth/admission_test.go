package auth

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestKDFAdmissionBoundsConcurrentAndQueuedWork(t *testing.T) {
	gate := newKDFAdmission(2, 1)
	t.Cleanup(func() {
		for len(gate.running) > 0 {
			gate.release()
		}
	})
	if err := gate.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := gate.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}

	queued := make(chan error, 1)
	go func() { queued <- gate.acquire(context.Background()) }()
	waitForAdmissionCount(t, gate.waiting, 1)

	if err := gate.acquire(context.Background()); !errors.Is(err, ErrKDFSaturated) {
		t.Fatalf("saturated acquire error = %v", err)
	}
	if got := len(gate.running); got != 2 {
		t.Fatalf("running=%d want=2", got)
	}
	if got := len(gate.waiting); got != 1 {
		t.Fatalf("waiting=%d want=1", got)
	}

	gate.release()
	if err := <-queued; err != nil {
		t.Fatalf("queued acquire: %v", err)
	}
	if got := len(gate.running); got != 2 {
		t.Fatalf("running after dequeue=%d want=2", got)
	}
	if got := len(gate.waiting); got != 0 {
		t.Fatalf("waiting after dequeue=%d want=0", got)
	}
}

func TestKDFAdmissionCancellationWhileWaitingReleasesCapacity(t *testing.T) {
	gate := newKDFAdmission(1, 1)
	if err := gate.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer gate.release()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- gate.acquire(ctx) }()
	waitForAdmissionCount(t, gate.waiting, 1)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire error = %v", err)
	}
	if got := len(gate.waiting); got != 0 {
		t.Fatalf("waiting after cancellation=%d want=0", got)
	}

	nextCtx, nextCancel := context.WithCancel(context.Background())
	next := make(chan error, 1)
	go func() { next <- gate.acquire(nextCtx) }()
	waitForAdmissionCount(t, gate.waiting, 1)
	nextCancel()
	if err := <-next; !errors.Is(err, context.Canceled) {
		t.Fatalf("replacement acquire error = %v", err)
	}
}

func TestKDFAdmissionReleaseAllowsReuse(t *testing.T) {
	gate := newKDFAdmission(1, 0)
	for i := 0; i < 3; i++ {
		if err := gate.acquire(context.Background()); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		gate.release()
	}
	if len(gate.running) != 0 || len(gate.waiting) != 0 {
		t.Fatalf("capacity retained: running=%d waiting=%d", len(gate.running), len(gate.waiting))
	}
}

func BenchmarkKDFAdmission(b *testing.B) {
	gate := newKDFAdmission(1, 0)
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := gate.acquire(ctx); err != nil {
			b.Fatal(err)
		}
		gate.release()
	}
}

func waitForAdmissionCount(t *testing.T, ch chan struct{}, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(ch) != want {
		if time.Now().After(deadline) {
			t.Fatalf("channel count=%d want=%d", len(ch), want)
		}
		runtime.Gosched()
	}
}
