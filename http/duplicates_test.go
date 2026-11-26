package fbhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yllix/stowr/v3/jobs"
	"github.com/yllix/stowr/v3/settings"
	"github.com/yllix/stowr/v3/users"
)

func writeBytes(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func pattern(size int, seed byte) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(i%251) ^ seed
	}
	return b
}

func startDuplicates(t *testing.T, handler http.Handler, token, target string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, target, http.NoBody)
	req.Header.Set("X-Auth", token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestDuplicatesFindsIdenticalFilesOnly(t *testing.T) {
	root := t.TempDir()
	const size = 300 << 10 // > 2 * quickHashChunk, so both passes run

	same := pattern(size, 1)
	writeBytes(t, root, "a/original.iso", same)
	writeBytes(t, root, "b/copy.iso", same)
	writeBytes(t, root, "b/deeper/another-copy.iso", same)

	// Same size, same first and last 64 KiB, different middle: the quick
	// pass matches, the full hash must not.
	tricky := pattern(size, 1)
	tricky[size/2] ^= 0xff
	writeBytes(t, root, "c/almost.iso", tricky)

	// Same size, different content.
	writeBytes(t, root, "c/other.iso", pattern(size, 7))

	// Identical but below the minimum size.
	writeBytes(t, root, "small1.txt", []byte("hello"))
	writeBytes(t, root, "small2.txt", []byte("hello"))

	useIndex(t, root)

	key := []byte("test-signing-key")
	perm := users.Permissions{}
	st := scopedUserStorage(t, root, perm, key)
	handler := handle(duplicatesHandler, "/api/duplicates", st, &settings.Server{Root: root})

	rec := startDuplicates(t, handler, signToken(t, perm, key), "/api/duplicates/?minSize=1024")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	var job jobs.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.Action != "duplicates" || job.Src != "/" {
		t.Fatalf("job = %+v", job)
	}

	done := waitForJob(t, 1, job.ID)
	if done.Status != jobs.StatusDone {
		t.Fatalf("job finished as %q (%s)", done.Status, done.Error)
	}
	res, ok := done.Result.(*DuplicatesResult)
	if !ok {
		t.Fatalf("result has type %T", done.Result)
	}
	if len(res.Groups) != 1 {
		t.Fatalf("groups = %+v, want exactly one", res.Groups)
	}
	g := res.Groups[0]
	if strings.Join(g.Paths, ",") != "/a/original.iso,/b/copy.iso,/b/deeper/another-copy.iso" {
		t.Fatalf("group paths = %v", g.Paths)
	}
	if g.Size != size || res.Wasted != 2*size {
		t.Fatalf("size = %d wasted = %d", g.Size, res.Wasted)
	}
	if done.DoneFiles != done.TotalFiles || done.TotalFiles != 5 {
		t.Fatalf("progress = %d of %d files", done.DoneFiles, done.TotalFiles)
	}

	// The job list stays light: no result payload.
	for _, s := range jobManager().List(1) {
		if s.ID == job.ID && s.Result != nil {
			t.Fatal("List included the result")
		}
	}
}

func TestDuplicatesScopedToFolder(t *testing.T) {
	root := t.TempDir()
	data := pattern(4096, 3)
	writeBytes(t, root, "photos/x.jpg", data)
	writeBytes(t, root, "photos/y.jpg", data)
	writeBytes(t, root, "music/z.jpg", data)
	useIndex(t, root)

	key := []byte("test-signing-key")
	perm := users.Permissions{}
	st := scopedUserStorage(t, root, perm, key)
	handler := handle(duplicatesHandler, "/api/duplicates", st, &settings.Server{Root: root})

	rec := startDuplicates(t, handler, signToken(t, perm, key), "/api/duplicates/photos?minSize=1")
	var job jobs.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	done := waitForJob(t, 1, job.ID)
	res := done.Result.(*DuplicatesResult)
	if len(res.Groups) != 1 || strings.Join(res.Groups[0].Paths, ",") != "/photos/x.jpg,/photos/y.jpg" {
		t.Fatalf("groups = %+v", res.Groups)
	}
}

func TestDuplicatesNeedsIndex(t *testing.T) {
	fileIndexMu.Lock()
	prev := fileIndex
	fileIndex = nil
	fileIndexMu.Unlock()
	t.Cleanup(func() {
		fileIndexMu.Lock()
		fileIndex = prev
		fileIndexMu.Unlock()
	})

	root := t.TempDir()
	key := []byte("test-signing-key")
	perm := users.Permissions{}
	st := scopedUserStorage(t, root, perm, key)
	handler := handle(duplicatesHandler, "/api/duplicates", st, &settings.Server{Root: root})

	if rec := startDuplicates(t, handler, signToken(t, perm, key), "/api/duplicates/"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestDuplicatesRejectsBadMinSize(t *testing.T) {
	root := t.TempDir()
	useIndex(t, root)
	key := []byte("test-signing-key")
	perm := users.Permissions{}
	st := scopedUserStorage(t, root, perm, key)
	handler := handle(duplicatesHandler, "/api/duplicates", st, &settings.Server{Root: root})

	for _, v := range []string{"0", "-5", "abc"} {
		if rec := startDuplicates(t, handler, signToken(t, perm, key), "/api/duplicates/?minSize="+v); rec.Code != http.StatusBadRequest {
			t.Fatalf("minSize=%s: status = %d, want 400", v, rec.Code)
		}
	}
}
