// Package jobs runs long file operations (copy, move) in the background so
// they are not tied to a single HTTP request, and tracks their progress so
// users can follow and cancel them.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Status is the lifecycle state of a job.
type Status string

const (
	StatusQueued   Status = "queued"
	StatusRunning  Status = "running"
	StatusDone     Status = "done"
	StatusFailed   Status = "failed"
	StatusCanceled Status = "canceled"
)

// Terminal reports whether s is a final state.
func (s Status) Terminal() bool {
	return s == StatusDone || s == StatusFailed || s == StatusCanceled
}

var (
	// ErrNotFound is returned when a job does not exist or belongs to
	// another user. The two cases are indistinguishable on purpose.
	ErrNotFound = errors.New("job not found")
	// ErrQueueFull is returned when too many jobs are already waiting.
	ErrQueueFull = errors.New("job queue is full")
)

// Func is the work a job performs. It must stop and return promptly once ctx
// is done, and may report progress through j.
type Func func(ctx context.Context, j *Job) error

// Job is a single background operation. Its exported methods are safe for
// concurrent use.
type Job struct {
	mu sync.Mutex

	id     string
	userID uint
	action string
	src    string
	dst    string

	status     Status
	err        string
	totalBytes int64
	doneBytes  int64
	totalFiles int
	doneFiles  int
	created    time.Time
	started    time.Time
	finished   time.Time

	ctx    context.Context
	cancel context.CancelFunc
	run    Func
}

// Snapshot is an immutable, JSON-friendly view of a job.
type Snapshot struct {
	ID         string     `json:"id"`
	Action     string     `json:"action"`
	Src        string     `json:"src"`
	Dst        string     `json:"dst"`
	Status     Status     `json:"status"`
	Error      string     `json:"error,omitempty"`
	TotalBytes int64      `json:"totalBytes"`
	DoneBytes  int64      `json:"doneBytes"`
	TotalFiles int        `json:"totalFiles"`
	DoneFiles  int        `json:"doneFiles"`
	Created    time.Time  `json:"created"`
	Started    *time.Time `json:"started,omitempty"`
	Finished   *time.Time `json:"finished,omitempty"`
}

// SetTotals records the expected amount of work.
func (j *Job) SetTotals(bytes int64, files int) {
	j.mu.Lock()
	j.totalBytes, j.totalFiles = bytes, files
	j.mu.Unlock()
}

// AddBytes records n more bytes processed.
func (j *Job) AddBytes(n int64) {
	j.mu.Lock()
	j.doneBytes += n
	j.mu.Unlock()
}

// FileDone records one more file processed.
func (j *Job) FileDone() {
	j.mu.Lock()
	j.doneFiles++
	j.mu.Unlock()
}

// Snapshot returns the current state of the job.
func (j *Job) Snapshot() Snapshot {
	j.mu.Lock()
	defer j.mu.Unlock()

	s := Snapshot{
		ID:         j.id,
		Action:     j.action,
		Src:        j.src,
		Dst:        j.dst,
		Status:     j.status,
		Error:      j.err,
		TotalBytes: j.totalBytes,
		DoneBytes:  j.doneBytes,
		TotalFiles: j.totalFiles,
		DoneFiles:  j.doneFiles,
		Created:    j.created,
	}
	if !j.started.IsZero() {
		t := j.started
		s.Started = &t
	}
	if !j.finished.IsZero() {
		t := j.finished
		s.Finished = &t
	}
	return s
}

// Manager queues jobs and runs them on a fixed pool of workers. Finished jobs
// are kept for a while so clients can read their outcome, then pruned.
type Manager struct {
	mu    sync.Mutex
	jobs  map[string]*Job
	queue chan *Job

	// Retention is how long a finished job stays visible.
	retention time.Duration
	// maxPerUser caps the finished jobs kept for one user; older ones are
	// pruned first.
	maxPerUser int

	now func() time.Time
}

// NewManager starts a manager with the given number of workers and a queue
// that holds up to queueSize waiting jobs.
func NewManager(workers, queueSize int) *Manager {
	if workers < 1 {
		workers = 1
	}
	if queueSize < 1 {
		queueSize = 1
	}

	m := &Manager{
		jobs:       make(map[string]*Job),
		queue:      make(chan *Job, queueSize),
		retention:  time.Hour,
		maxPerUser: 50,
		now:        time.Now,
	}
	for range workers {
		go m.worker()
	}
	return m
}

// Submit queues fn as a new job owned by userID.
func (m *Manager) Submit(userID uint, action, src, dst string, fn Func) (Snapshot, error) {
	id, err := newID()
	if err != nil {
		return Snapshot{}, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{
		id:      id,
		userID:  userID,
		action:  action,
		src:     src,
		dst:     dst,
		status:  StatusQueued,
		created: m.now(),
		ctx:     ctx,
		cancel:  cancel,
		run:     fn,
	}

	m.mu.Lock()
	m.pruneLocked()
	m.jobs[id] = j
	m.mu.Unlock()

	select {
	case m.queue <- j:
		return j.Snapshot(), nil
	default:
		m.mu.Lock()
		delete(m.jobs, id)
		m.mu.Unlock()
		cancel()
		return Snapshot{}, ErrQueueFull
	}
}

// List returns the jobs owned by userID, newest first.
func (m *Manager) List(userID uint) []Snapshot {
	m.mu.Lock()
	m.pruneLocked()
	var owned []*Job
	for _, j := range m.jobs {
		if j.userID == userID {
			owned = append(owned, j)
		}
	}
	m.mu.Unlock()

	out := make([]Snapshot, 0, len(owned))
	for _, j := range owned {
		out = append(out, j.Snapshot())
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Created.After(out[b].Created) })
	return out
}

// Get returns one job owned by userID.
func (m *Manager) Get(userID uint, id string) (Snapshot, error) {
	j, err := m.lookup(userID, id)
	if err != nil {
		return Snapshot{}, err
	}
	return j.Snapshot(), nil
}

// Cancel stops a job owned by userID. Cancelling a finished job is a no-op.
func (m *Manager) Cancel(userID uint, id string) (Snapshot, error) {
	j, err := m.lookup(userID, id)
	if err != nil {
		return Snapshot{}, err
	}

	j.mu.Lock()
	if j.status == StatusQueued {
		// The worker skips jobs that were cancelled while waiting.
		j.status = StatusCanceled
		j.finished = m.now()
	}
	j.mu.Unlock()
	j.cancel()

	return j.Snapshot(), nil
}

func (m *Manager) lookup(userID uint, id string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j.userID != userID {
		return nil, ErrNotFound
	}
	return j, nil
}

func (m *Manager) worker() {
	for j := range m.queue {
		m.execute(j)
	}
}

func (m *Manager) execute(j *Job) {
	j.mu.Lock()
	if j.status != StatusQueued {
		j.mu.Unlock()
		return
	}
	j.status = StatusRunning
	j.started = m.now()
	j.mu.Unlock()

	err := safeRun(j)

	j.mu.Lock()
	defer j.mu.Unlock()
	j.finished = m.now()
	// Go by what the work returned, not by whether the context was
	// cancelled: a job that completed just as it was cancelled is done.
	switch {
	case err == nil:
		j.status = StatusDone
	case errors.Is(err, context.Canceled) && j.ctx.Err() != nil:
		j.status = StatusCanceled
	default:
		j.status = StatusFailed
		j.err = err.Error()
	}
	j.cancel()
}

// safeRun runs the job, turning a panic into a job failure instead of
// crashing the server.
func safeRun(j *Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error: %v", r)
		}
	}()
	return j.run(j.ctx, j)
}

// pruneLocked drops finished jobs past the retention period, and the oldest
// finished jobs of any user over maxPerUser. m.mu must be held.
func (m *Manager) pruneLocked() {
	type entry struct {
		id   string
		done time.Time
	}

	cutoff := m.now().Add(-m.retention)
	finished := make(map[uint][]entry)

	for id, j := range m.jobs {
		j.mu.Lock()
		terminal := j.status.Terminal()
		done := j.finished
		j.mu.Unlock()

		if !terminal {
			continue
		}
		if done.Before(cutoff) {
			delete(m.jobs, id)
			continue
		}
		finished[j.userID] = append(finished[j.userID], entry{id: id, done: done})
	}

	for _, list := range finished {
		if len(list) <= m.maxPerUser {
			continue
		}
		sort.Slice(list, func(a, b int) bool { return list[a].done.After(list[b].done) })
		for _, e := range list[m.maxPerUser:] {
			delete(m.jobs, e.id)
		}
	}
}

func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
