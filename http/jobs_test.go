package fbhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yllix/stowr/v3/diskcache"
	"github.com/yllix/stowr/v3/jobs"
	"github.com/yllix/stowr/v3/settings"
	"github.com/yllix/stowr/v3/users"
)

func asyncPatch(t *testing.T, handler http.Handler, signed, src, action, dst string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, src, http.NoBody)
	q := req.URL.Query()
	q.Set("action", action)
	q.Set("destination", dst)
	q.Set("async", "true")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("X-Auth", signed)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func waitForJob(t *testing.T, userID uint, id string) jobs.Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, err := jobManager().Get(userID, id)
		if err != nil {
			t.Fatalf("job lookup: %v", err)
		}
		if s.Status.Terminal() {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish", id)
	return jobs.Snapshot{}
}

func TestAsyncCopyRunsAsJob(t *testing.T) {
	userScope := t.TempDir()
	if err := os.MkdirAll(filepath.Join(userScope, "src", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userScope, "src", "sub", "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Rename: true, Modify: true}
	st := scopedUserStorage(t, userScope, perm, key)
	handler := handle(resourcePatchHandler(diskcache.NewNoOp()), "", st, &settings.Server{})

	rec := asyncPatch(t, handler, signToken(t, perm, key), "/src", "copy", "/dst")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%q", rec.Code, rec.Body.String())
	}

	var job jobs.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if job.ID == "" || job.Action != "copy" || job.Src != "/src" || job.Dst != "/dst" {
		t.Fatalf("unexpected job: %+v", job)
	}

	done := waitForJob(t, 1, job.ID)
	if done.Status != jobs.StatusDone {
		t.Fatalf("job finished as %q (%s)", done.Status, done.Error)
	}
	if done.DoneBytes != 5 || done.TotalBytes != 5 || done.DoneFiles != 1 {
		t.Fatalf("progress = %+v", done)
	}

	got, err := os.ReadFile(filepath.Join(userScope, "dst", "sub", "f.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("copied file = %q, %v", got, err)
	}
}

func TestAsyncMoveRunsAsJob(t *testing.T) {
	userScope := t.TempDir()
	if err := os.WriteFile(filepath.Join(userScope, "a.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Rename: true, Modify: true}
	st := scopedUserStorage(t, userScope, perm, key)
	handler := handle(resourcePatchHandler(diskcache.NewNoOp()), "", st, &settings.Server{})

	rec := asyncPatch(t, handler, signToken(t, perm, key), "/a.txt", "rename", "/b.txt")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%q", rec.Code, rec.Body.String())
	}
	var job jobs.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}

	if done := waitForJob(t, 1, job.ID); done.Status != jobs.StatusDone {
		t.Fatalf("job finished as %q (%s)", done.Status, done.Error)
	}
	if _, err := os.Stat(filepath.Join(userScope, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("source still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(userScope, "b.txt")); err != nil {
		t.Fatalf("destination missing: %v", err)
	}
}

func TestAsyncCopyRequiresCreatePermission(t *testing.T) {
	userScope := t.TempDir()
	if err := os.WriteFile(filepath.Join(userScope, "a.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Rename: true}
	st := scopedUserStorage(t, userScope, perm, key)
	handler := handle(resourcePatchHandler(diskcache.NewNoOp()), "", st, &settings.Server{})

	rec := asyncPatch(t, handler, signToken(t, perm, key), "/a.txt", "copy", "/b.txt")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(userScope, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("copy happened without permission: %v", err)
	}
}

func TestAsyncCopyConflictIsCheckedUpFront(t *testing.T) {
	userScope := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(userScope, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Rename: true, Modify: true}
	st := scopedUserStorage(t, userScope, perm, key)
	handler := handle(resourcePatchHandler(diskcache.NewNoOp()), "", st, &settings.Server{})

	rec := asyncPatch(t, handler, signToken(t, perm, key), "/a.txt", "copy", "/b.txt")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if got, _ := os.ReadFile(filepath.Join(userScope, "b.txt")); string(got) != "b.txt" {
		t.Fatalf("existing destination was modified: %q", got)
	}
}

func TestPublicJobErrorHidesServerPaths(t *testing.T) {
	_, err := os.Open("/definitely/not/here/secret-path")
	msg := publicJobError(err).Error()
	if msg != "not found" {
		t.Fatalf("message = %q, want %q", msg, "not found")
	}
}
