package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ── Visibility ────────────────────────────────────────────────────────────────

// JobVisibility controls who can see (and potentially cancel) a job.
type JobVisibility int

const (
	// VisibilityGlobal — every authenticated user can see this job.
	VisibilityGlobal JobVisibility = iota
	// VisibilityUser — only the owning user (and admins) can see this job.
	VisibilityUser
	// VisibilityRole — only users whose level >= MinRoleLevel (and admins) can see it.
	VisibilityRole
)

func (v JobVisibility) String() string {
	switch v {
	case VisibilityGlobal:
		return "global"
	case VisibilityUser:
		return "user"
	case VisibilityRole:
		return "role"
	default:
		return "unknown"
	}
}

func (v JobVisibility) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.String())
}

// ── Status ────────────────────────────────────────────────────────────────────

// JobStatus represents the lifecycle state of a job.
type JobStatus int

const (
	StatusPending   JobStatus = iota // registered but work hasn't started
	StatusRunning                     // actively processing
	StatusDone                        // completed successfully
	StatusFailed                      // finished with an error
	StatusCancelled                   // cancelled by a user/admin
)

func (s JobStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusRunning:
		return "running"
	case StatusDone:
		return "done"
	case StatusFailed:
		return "failed"
	case StatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

func (s JobStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// IsTerminal returns true if the job has entered a final state.
func (s JobStatus) IsTerminal() bool {
	return s == StatusDone || s == StatusFailed || s == StatusCancelled
}

// ── Job ───────────────────────────────────────────────────────────────────────

// Job represents a single long-running operation tracked by the JobManager.
// All exported fields are safe to read under the manager's lock.
type Job struct {
	mu sync.RWMutex

	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Status      JobStatus     `json:"status"`
	Visibility  JobVisibility `json:"visibility"`

	// OwnerUserID is set when Visibility == VisibilityUser.
	OwnerUserID int32 `json:"owner_user_id,omitempty"`
	// OwnerUserName is the display name of the owning user (informational).
	OwnerUserName string `json:"owner_user_name,omitempty"`
	// MinRoleLevel is set when Visibility == VisibilityRole.
	MinRoleLevel int32 `json:"min_role_level,omitempty"`

	// Progress tracking.
	Progress int `json:"progress"` // items completed
	Total    int `json:"total"`    // total items (0 = indeterminate)
	Message  string `json:"message"`  // free-form status text

	// Rate & ETA (computed).
	RatePerSec float64 `json:"rate_per_sec"`
	ETASec     int     `json:"eta_sec"` // -1 = unknown

	// Timestamps.
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	// Error holds the failure reason when Status == StatusFailed.
	Error string `json:"error,omitempty"`

	// Internal — not serialised.
	cancel context.CancelFunc
	ctx    context.Context
	mgr    *JobManager // back-pointer for broadcasting
}

// Ctx returns the job's context.  Workers should select on Ctx().Done() to
// support cancellation.
func (j *Job) Ctx() context.Context {
	return j.ctx
}

// SetProgress updates the progress counters and recomputes rate/ETA.
// It also broadcasts the updated snapshot to SSE subscribers.
func (j *Job) SetProgress(current, total int, message string) {
	j.mu.Lock()
	j.Progress = current
	j.Total = total
	j.Message = message
	j.recomputeRate()
	j.mu.Unlock()

	if j.mgr != nil {
		j.mgr.broadcast()
	}
}

// IncrementProgress adds delta to the current progress and updates the message.
func (j *Job) IncrementProgress(delta int, message string) {
	j.mu.Lock()
	j.Progress += delta
	if message != "" {
		j.Message = message
	}
	j.recomputeRate()
	j.mu.Unlock()

	if j.mgr != nil {
		j.mgr.broadcast()
	}
}

// SetTotal updates the total count (useful when total is discovered during work).
func (j *Job) SetTotal(total int) {
	j.mu.Lock()
	j.Total = total
	j.recomputeRate()
	j.mu.Unlock()

	if j.mgr != nil {
		j.mgr.broadcast()
	}
}

// Start transitions the job from Pending to Running.
func (j *Job) Start() {
	j.mu.Lock()
	if j.Status == StatusPending {
		j.Status = StatusRunning
		now := time.Now()
		j.StartedAt = &now
	}
	j.mu.Unlock()

	if j.mgr != nil {
		j.mgr.broadcast()
	}
}

// Complete marks the job as done with an optional final message.
func (j *Job) Complete(message string) {
	j.mu.Lock()
	j.Status = StatusDone
	if message != "" {
		j.Message = message
	}
	now := time.Now()
	j.FinishedAt = &now
	j.mu.Unlock()

	if j.mgr != nil {
		j.mgr.broadcast()
		j.mgr.scheduleCleanup(j.ID)
	}
}

// Fail marks the job as failed with an error message.
func (j *Job) Fail(errMsg string) {
	j.mu.Lock()
	j.Status = StatusFailed
	j.Error = errMsg
	now := time.Now()
	j.FinishedAt = &now
	j.mu.Unlock()

	if j.mgr != nil {
		j.mgr.broadcast()
		j.mgr.scheduleCleanup(j.ID)
	}
}

// recomputeRate recalculates RatePerSec and ETASec.  Must be called with mu held.
func (j *Job) recomputeRate() {
	j.RatePerSec = 0
	j.ETASec = -1

	if j.StartedAt == nil || j.Progress == 0 {
		return
	}

	elapsed := time.Since(*j.StartedAt).Seconds()
	if elapsed <= 0 {
		return
	}

	j.RatePerSec = math.Round(float64(j.Progress)/elapsed*100) / 100

	remaining := j.Total - j.Progress
	if remaining > 0 && j.RatePerSec > 0 {
		j.ETASec = int(math.Ceil(float64(remaining) / j.RatePerSec))
	} else if remaining <= 0 {
		j.ETASec = 0
	}
}

// Snapshot returns a JSON-safe copy of the job's current state.
func (j *Job) Snapshot() JobSnapshot {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return JobSnapshot{
		ID:            j.ID,
		Name:          j.Name,
		Description:   j.Description,
		Status:        j.Status,
		Visibility:    j.Visibility,
		OwnerUserID:   j.OwnerUserID,
		OwnerUserName: j.OwnerUserName,
		MinRoleLevel:  j.MinRoleLevel,
		Progress:      j.Progress,
		Total:         j.Total,
		Message:       j.Message,
		RatePerSec:    j.RatePerSec,
		ETASec:        j.ETASec,
		CreatedAt:     j.CreatedAt,
		StartedAt:     j.StartedAt,
		FinishedAt:    j.FinishedAt,
		Error:         j.Error,
	}
}

// ── JobSnapshot ───────────────────────────────────────────────────────────────

// JobSnapshot is a JSON-serialisable, read-only view of a Job.
type JobSnapshot struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	Status        JobStatus     `json:"status"`
	Visibility    JobVisibility `json:"visibility"`
	OwnerUserID   int32         `json:"owner_user_id,omitempty"`
	OwnerUserName string        `json:"owner_user_name,omitempty"`
	MinRoleLevel  int32         `json:"min_role_level,omitempty"`
	Progress      int           `json:"progress"`
	Total         int           `json:"total"`
	Message       string        `json:"message"`
	RatePerSec    float64       `json:"rate_per_sec"`
	ETASec        int           `json:"eta_sec"`
	CreatedAt     time.Time     `json:"created_at"`
	StartedAt     *time.Time    `json:"started_at,omitempty"`
	FinishedAt    *time.Time    `json:"finished_at,omitempty"`
	Error         string        `json:"error,omitempty"`
}

// ── JobManager ────────────────────────────────────────────────────────────────

// jobCleanupTTL is how long a finished job survives before being evicted.
const jobCleanupTTL = 30 * time.Minute

// JobManager is the central registry of all tracked jobs.
type JobManager struct {
	mu   sync.RWMutex
	jobs map[string]*Job // keyed by Job.ID

	// SSE subscriber registry.
	subMu sync.RWMutex
	subs  []chan []JobSnapshot
}

// NewJobManager creates a new, empty job manager.
func NewJobManager() *JobManager {
	return &JobManager{
		jobs: make(map[string]*Job),
	}
}

// JobOpts holds optional configuration when registering a new job.
type JobOpts struct {
	Description   string
	Visibility    JobVisibility
	OwnerUserID   int32
	OwnerUserName string
	MinRoleLevel  int32
	Total         int // estimated total (0 = indeterminate)
}

// Register creates and stores a new Job, returning a pointer the caller uses
// to update progress and signal completion.
func (m *JobManager) Register(name string, opts JobOpts) *Job {
	ctx, cancel := context.WithCancel(context.Background())

	j := &Job{
		ID:            uuid.New().String(),
		Name:          name,
		Description:   opts.Description,
		Status:        StatusPending,
		Visibility:    opts.Visibility,
		OwnerUserID:   opts.OwnerUserID,
		OwnerUserName: opts.OwnerUserName,
		MinRoleLevel:  opts.MinRoleLevel,
		Total:         opts.Total,
		ETASec:        -1,
		CreatedAt:     time.Now(),
		cancel:        cancel,
		ctx:           ctx,
		mgr:           m,
	}

	m.mu.Lock()
	m.jobs[j.ID] = j
	m.mu.Unlock()

	m.broadcast()
	return j
}

// Get returns a job by ID, or nil if not found.
func (m *JobManager) Get(id string) *Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobs[id]
}

// Cancel requests cancellation of a job.  Returns an error if the job is not
// found or already in a terminal state.
func (m *JobManager) Cancel(id string) error {
	m.mu.RLock()
	j := m.jobs[id]
	m.mu.RUnlock()

	if j == nil {
		return fmt.Errorf("job %q not found", id)
	}

	j.mu.Lock()
	if j.Status.IsTerminal() {
		j.mu.Unlock()
		return fmt.Errorf("job %q already %s", id, j.Status)
	}
	j.Status = StatusCancelled
	now := time.Now()
	j.FinishedAt = &now
	j.cancel()
	j.mu.Unlock()

	m.broadcast()
	m.scheduleCleanup(id)
	return nil
}

// ListAll returns snapshots of every job, ordered newest first.
func (m *JobManager) ListAll() []JobSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]JobSnapshot, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j.Snapshot())
	}
	// Sort newest first.
	sortSnapshotsDesc(out)
	return out
}

// ListVisible returns snapshots visible to the given viewer.
func (m *JobManager) ListVisible(viewerUserID int32, viewerLevel int32) []JobSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]JobSnapshot, 0, len(m.jobs))
	for _, j := range m.jobs {
		j.mu.RLock()
		vis := j.Visibility
		owner := j.OwnerUserID
		minRole := j.MinRoleLevel
		j.mu.RUnlock()

		switch vis {
		case VisibilityGlobal:
			out = append(out, j.Snapshot())
		case VisibilityUser:
			if viewerUserID == owner || viewerLevel >= LevelAdmin {
				out = append(out, j.Snapshot())
			}
		case VisibilityRole:
			if viewerLevel >= minRole || viewerLevel >= LevelAdmin {
				out = append(out, j.Snapshot())
			}
		}
	}
	sortSnapshotsDesc(out)
	return out
}

// ── SSE fan-out ───────────────────────────────────────────────────────────────

// Subscribe returns a channel that receives snapshots on every change.
func (m *JobManager) Subscribe() chan []JobSnapshot {
	ch := make(chan []JobSnapshot, 8)
	m.subMu.Lock()
	m.subs = append(m.subs, ch)
	m.subMu.Unlock()
	return ch
}

// Unsubscribe removes and closes a subscriber channel.
func (m *JobManager) Unsubscribe(ch chan []JobSnapshot) {
	m.subMu.Lock()
	for i, s := range m.subs {
		if s == ch {
			m.subs = append(m.subs[:i], m.subs[i+1:]...)
			break
		}
	}
	m.subMu.Unlock()
	close(ch)
}

func (m *JobManager) broadcast() {
	all := m.ListAll()
	m.subMu.RLock()
	for _, ch := range m.subs {
		select {
		case ch <- all:
		default:
		}
	}
	m.subMu.RUnlock()
}

// ── cleanup ──────────────────────────────────────────────────────────────────

func (m *JobManager) scheduleCleanup(id string) {
	go func() {
		time.Sleep(jobCleanupTTL)
		m.mu.Lock()
		if j, ok := m.jobs[id]; ok {
			j.mu.RLock()
			terminal := j.Status.IsTerminal()
			j.mu.RUnlock()
			if terminal {
				delete(m.jobs, id)
			}
		}
		m.mu.Unlock()
		m.broadcast()
	}()
}

// ── sorting helper ───────────────────────────────────────────────────────────

func sortSnapshotsDesc(s []JobSnapshot) {
	// Simple insertion sort — job lists are tiny.
	for i := 1; i < len(s); i++ {
		for k := i; k > 0 && s[k].CreatedAt.After(s[k-1].CreatedAt); k-- {
			s[k], s[k-1] = s[k-1], s[k]
		}
	}
}
