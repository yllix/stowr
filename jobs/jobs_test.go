package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

// waitFor polls until the job reaches a terminal state or the test times out.
func waitFor(t *testing.T, m *Manager, user uint, id string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, err := m.Get(user, id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if s.Status.Terminal() {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish in time", id)
	return Snapshot{}
}

func TestJobSucceedsAndReportsProgress(t *testing.T) {
	m := NewManager(1, 8)

	s, err := m.Submit(1, "copy", "/a", "/b", func(_ context.Context, j *Job) error {
		j.SetTotals(30, 3)
		for range 3 {
			j.AddBytes(10)
			j.FileDone()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if s.Status != StatusQueued {
		t.Fatalf("new job status = %q, want queued", s.Status)
	}

	got := waitFor(t, m, 1, s.ID)
	if got.Status != StatusDone {
		t.Fatalf("status = %q, want done", got.Status)
	}
	if got.DoneBytes != 30 || got.TotalBytes != 30 || got.DoneFiles != 3 || got.TotalFiles != 3 {
		t.Fatalf("progress = %+v", got)
	}
	if got.Started == nil || got.Finished == nil {
		t.Fatalf("missing timestamps: %+v", got)
	}
	if got.Src != "/a" || got.Dst != "/b" || got.Action != "copy" {
		t.Fatalf("metadata = %+v", got)
	}
}

func TestJobFailureIsRecorded(t *testing.T) {
	m := NewManager(1, 8)
	s, _ := m.Submit(1, "copy", "/a", "/b", func(context.Context, *Job) error {
		return errors.New("permission denied")
	})

	got := waitFor(t, m, 1, s.ID)
	if got.Status != StatusFailed || got.Error != "permission denied" {
		t.Fatalf("got %+v, want failed with error", got)
	}
}

func TestJobPanicBecomesFailure(t *testing.T) {
	m := NewManager(1, 8)
	s, _ := m.Submit(1, "copy", "/a", "/b", func(context.Context, *Job) error {
		panic("boom")
	})

	got := waitFor(t, m, 1, s.ID)
	if got.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
}

func TestCancelRunningJob(t *testing.T) {
	m := NewManager(1, 8)
	started := make(chan struct{})

	s, _ := m.Submit(1, "copy", "/a", "/b", func(ctx context.Context, _ *Job) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})

	<-started
	if _, err := m.Cancel(1, s.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got := waitFor(t, m, 1, s.ID)
	if got.Status != StatusCanceled {
		t.Fatalf("status = %q, want canceled", got.Status)
	}
	if got.Error != "" {
		t.Fatalf("canceled job should not report an error, got %q", got.Error)
	}
}

func TestCancelQueuedJobNeverRuns(t *testing.T) {
	m := NewManager(1, 8)
	release := make(chan struct{})
	ran := make(chan struct{}, 1)

	// Occupy the only worker.
	blocker, _ := m.Submit(1, "copy", "/x", "/y", func(context.Context, *Job) error {
		<-release
		return nil
	})
	queued, _ := m.Submit(1, "copy", "/a", "/b", func(context.Context, *Job) error {
		ran <- struct{}{}
		return nil
	})

	if _, err := m.Cancel(1, queued.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	close(release)
	waitFor(t, m, 1, blocker.ID)

	got := waitFor(t, m, 1, queued.ID)
	if got.Status != StatusCanceled {
		t.Fatalf("status = %q, want canceled", got.Status)
	}
	select {
	case <-ran:
		t.Fatal("cancelled queued job still ran")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestJobsAreScopedToTheirOwner(t *testing.T) {
	m := NewManager(1, 8)
	s, _ := m.Submit(1, "copy", "/a", "/b", func(context.Context, *Job) error { return nil })
	waitFor(t, m, 1, s.ID)

	if _, err := m.Get(2, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user Get err = %v, want ErrNotFound", err)
	}
	if _, err := m.Cancel(2, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user Cancel err = %v, want ErrNotFound", err)
	}
	if list := m.List(2); len(list) != 0 {
		t.Fatalf("other user sees %d jobs, want 0", len(list))
	}
	if list := m.List(1); len(list) != 1 || list[0].ID != s.ID {
		t.Fatalf("owner list = %+v", list)
	}
}

func TestQueueFull(t *testing.T) {
	m := NewManager(1, 1)
	release := make(chan struct{})
	defer close(release)
	running := make(chan struct{})

	block := func(context.Context, *Job) error {
		<-release
		return nil
	}

	// First job occupies the worker, second fills the queue.
	if _, err := m.Submit(1, "copy", "/a", "/b", func(ctx context.Context, j *Job) error {
		close(running)
		return block(ctx, j)
	}); err != nil {
		t.Fatalf("Submit 1: %v", err)
	}
	<-running
	if _, err := m.Submit(1, "copy", "/a", "/b", block); err != nil {
		t.Fatalf("Submit 2: %v", err)
	}
	if _, err := m.Submit(1, "copy", "/a", "/b", block); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("Submit 3 err = %v, want ErrQueueFull", err)
	}
	if got := len(m.List(1)); got != 2 {
		t.Fatalf("rejected job should not be listed: %d jobs", got)
	}
}

func TestFinishedJobsArePruned(t *testing.T) {
	m := NewManager(1, 8)
	s, _ := m.Submit(1, "copy", "/a", "/b", func(context.Context, *Job) error { return nil })
	waitFor(t, m, 1, s.ID)

	m.mu.Lock()
	m.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	m.mu.Unlock()

	if list := m.List(1); len(list) != 0 {
		t.Fatalf("expired job still listed: %+v", list)
	}
}
