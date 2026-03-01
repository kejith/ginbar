package api

import (
	"sync"
	"testing"
	"time"
)

func TestJobManager_RegisterAndGet(t *testing.T) {
	m := NewJobManager()
	j := m.Register("test-job", JobOpts{
		Description: "a test job",
		Visibility:  VisibilityGlobal,
		Total:       100,
	})

	if j.ID == "" {
		t.Fatal("expected non-empty job ID")
	}
	if j.Name != "test-job" {
		t.Errorf("expected name %q, got %q", "test-job", j.Name)
	}
	if j.Status != StatusPending {
		t.Errorf("expected status Pending, got %s", j.Status)
	}

	got := m.Get(j.ID)
	if got == nil {
		t.Fatal("Get returned nil for a registered job")
	}
	if got.ID != j.ID {
		t.Errorf("expected ID %q, got %q", j.ID, got.ID)
	}

	// Not found.
	if m.Get("nonexistent") != nil {
		t.Error("expected nil for nonexistent job")
	}
}

func TestJob_Lifecycle(t *testing.T) {
	m := NewJobManager()
	j := m.Register("lifecycle", JobOpts{Total: 10})

	// Start.
	j.Start()
	if j.Status != StatusRunning {
		t.Errorf("expected Running, got %s", j.Status)
	}
	if j.StartedAt == nil {
		t.Error("expected StartedAt to be set")
	}

	// Progress.
	j.SetProgress(5, 10, "halfway")
	snap := j.Snapshot()
	if snap.Progress != 5 || snap.Total != 10 || snap.Message != "halfway" {
		t.Errorf("unexpected progress: %+v", snap)
	}

	// Complete.
	j.Complete("done!")
	if j.Status != StatusDone {
		t.Errorf("expected Done, got %s", j.Status)
	}
	if j.FinishedAt == nil {
		t.Error("expected FinishedAt to be set")
	}
}

func TestJob_IncrementProgress(t *testing.T) {
	m := NewJobManager()
	j := m.Register("incr", JobOpts{Total: 10})
	j.Start()

	j.IncrementProgress(3, "three done")
	j.IncrementProgress(2, "five done")

	snap := j.Snapshot()
	if snap.Progress != 5 {
		t.Errorf("expected progress 5, got %d", snap.Progress)
	}
	if snap.Message != "five done" {
		t.Errorf("expected message %q, got %q", "five done", snap.Message)
	}
}

func TestJob_Fail(t *testing.T) {
	m := NewJobManager()
	j := m.Register("will-fail", JobOpts{})
	j.Start()
	j.Fail("something broke")

	if j.Status != StatusFailed {
		t.Errorf("expected Failed, got %s", j.Status)
	}
	if j.Error != "something broke" {
		t.Errorf("unexpected error: %q", j.Error)
	}
}

func TestJobManager_Cancel(t *testing.T) {
	m := NewJobManager()
	j := m.Register("cancel-me", JobOpts{})
	j.Start()

	if err := m.Cancel(j.ID); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if j.Status != StatusCancelled {
		t.Errorf("expected Cancelled, got %s", j.Status)
	}

	// Context should be done.
	select {
	case <-j.Ctx().Done():
		// good
	default:
		t.Error("expected job context to be cancelled")
	}

	// Cancel again should fail.
	if err := m.Cancel(j.ID); err == nil {
		t.Error("expected error on second cancel")
	}

	// Cancel nonexistent.
	if err := m.Cancel("no-such-id"); err == nil {
		t.Error("expected error for nonexistent job")
	}
}

func TestJobManager_ListAll(t *testing.T) {
	m := NewJobManager()
	m.Register("one", JobOpts{})
	m.Register("two", JobOpts{})
	m.Register("three", JobOpts{})

	jobs := m.ListAll()
	if len(jobs) != 3 {
		t.Errorf("expected 3 jobs, got %d", len(jobs))
	}

	// Should be sorted newest first.
	for i := 1; i < len(jobs); i++ {
		if jobs[i].CreatedAt.After(jobs[i-1].CreatedAt) {
			t.Error("jobs not sorted newest-first")
		}
	}
}

func TestJobManager_ListVisible_Global(t *testing.T) {
	m := NewJobManager()
	m.Register("global-job", JobOpts{Visibility: VisibilityGlobal})

	jobs := m.ListVisible(999, LevelMember)
	if len(jobs) != 1 {
		t.Errorf("expected 1 visible job for any user, got %d", len(jobs))
	}
}

func TestJobManager_ListVisible_User(t *testing.T) {
	m := NewJobManager()
	m.Register("owner-job", JobOpts{
		Visibility:  VisibilityUser,
		OwnerUserID: 42,
	})

	// Owner can see it.
	jobs := m.ListVisible(42, LevelMember)
	if len(jobs) != 1 {
		t.Errorf("owner should see their job, got %d", len(jobs))
	}

	// Other user cannot.
	jobs = m.ListVisible(99, LevelMember)
	if len(jobs) != 0 {
		t.Errorf("other user should not see job, got %d", len(jobs))
	}

	// Admin can see it.
	jobs = m.ListVisible(99, LevelAdmin)
	if len(jobs) != 1 {
		t.Errorf("admin should see all jobs, got %d", len(jobs))
	}
}

func TestJobManager_ListVisible_Role(t *testing.T) {
	m := NewJobManager()
	m.Register("secret-job", JobOpts{
		Visibility:   VisibilityRole,
		MinRoleLevel: LevelSecret,
	})

	// Low-level user cannot see it.
	jobs := m.ListVisible(1, LevelMember)
	if len(jobs) != 0 {
		t.Errorf("member should not see secret job, got %d", len(jobs))
	}

	// Matching role can see it.
	jobs = m.ListVisible(1, LevelSecret)
	if len(jobs) != 1 {
		t.Errorf("secret-level user should see job, got %d", len(jobs))
	}

	// Admin can see it.
	jobs = m.ListVisible(1, LevelAdmin)
	if len(jobs) != 1 {
		t.Errorf("admin should see job, got %d", len(jobs))
	}
}

func TestJobManager_Subscribe(t *testing.T) {
	m := NewJobManager()
	ch := m.Subscribe()

	// Registering a job should send a broadcast.
	m.Register("broadcast-test", JobOpts{})

	select {
	case snaps := <-ch:
		if len(snaps) != 1 {
			t.Errorf("expected 1 job in broadcast, got %d", len(snaps))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast")
	}

	m.Unsubscribe(ch)

	// Channel should be closed after unsubscribe.
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed after unsubscribe")
	}
}

func TestJob_ETA(t *testing.T) {
	m := NewJobManager()
	j := m.Register("eta-test", JobOpts{Total: 100})
	j.Start()

	// Manually set start time to 10 seconds ago to get a stable rate.
	j.mu.Lock()
	past := time.Now().Add(-10 * time.Second)
	j.StartedAt = &past
	j.mu.Unlock()

	j.SetProgress(50, 100, "halfway")
	snap := j.Snapshot()

	if snap.RatePerSec <= 0 {
		t.Error("expected positive rate")
	}
	if snap.ETASec <= 0 {
		t.Error("expected positive ETA")
	}
	// At 5/sec with 50 remaining, ETA should be ~10s.
	if snap.ETASec < 8 || snap.ETASec > 12 {
		t.Errorf("expected ETA ~10s, got %d", snap.ETASec)
	}
}

func TestJobStatus_MarshalJSON(t *testing.T) {
	cases := []struct {
		s    JobStatus
		want string
	}{
		{StatusPending, `"pending"`},
		{StatusRunning, `"running"`},
		{StatusDone, `"done"`},
		{StatusFailed, `"failed"`},
		{StatusCancelled, `"cancelled"`},
	}
	for _, tc := range cases {
		got, err := tc.s.MarshalJSON()
		if err != nil {
			t.Errorf("MarshalJSON(%s) error: %v", tc.s, err)
		}
		if string(got) != tc.want {
			t.Errorf("MarshalJSON(%s) = %s, want %s", tc.s, string(got), tc.want)
		}
	}
}

func TestJobVisibility_MarshalJSON(t *testing.T) {
	cases := []struct {
		v    JobVisibility
		want string
	}{
		{VisibilityGlobal, `"global"`},
		{VisibilityUser, `"user"`},
		{VisibilityRole, `"role"`},
	}
	for _, tc := range cases {
		got, err := tc.v.MarshalJSON()
		if err != nil {
			t.Errorf("MarshalJSON(%s) error: %v", tc.v, err)
		}
		if string(got) != tc.want {
			t.Errorf("MarshalJSON(%s) = %s, want %s", tc.v, string(got), tc.want)
		}
	}
}

func TestJob_ConcurrentProgress(t *testing.T) {
	m := NewJobManager()
	j := m.Register("concurrent", JobOpts{Total: 1000})
	j.Start()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j.IncrementProgress(10, "")
		}()
	}
	wg.Wait()

	snap := j.Snapshot()
	if snap.Progress != 1000 {
		t.Errorf("expected progress 1000, got %d", snap.Progress)
	}
}
