package fbhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yllix/stowr/v3/index"
	"github.com/yllix/stowr/v3/settings"
	"github.com/yllix/stowr/v3/users"
)

// useIndex installs a freshly built index of root for the duration of t.
func useIndex(t *testing.T, root string) *index.Index {
	t.Helper()
	ix := index.New(root, time.Hour)
	if err := ix.Build(context.Background()); err != nil {
		t.Fatalf("index build: %v", err)
	}
	fileIndexMu.Lock()
	prev := fileIndex
	fileIndex = ix
	fileIndexMu.Unlock()
	t.Cleanup(func() {
		fileIndexMu.Lock()
		fileIndex = prev
		fileIndexMu.Unlock()
	})
	return ix
}

func writeFile(t *testing.T, root, rel string, size int) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInsightsReportsFolderSizes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "media/movie.mkv", 3000)
	writeFile(t, root, "media/song.mp3", 500)
	writeFile(t, root, "docs/a.txt", 20)
	useIndex(t, root)

	key := []byte("test-signing-key")
	perm := users.Permissions{}
	st := scopedUserStorage(t, root, perm, key)
	handler := handle(insightsHandler, "/api/insights", st, &settings.Server{Root: root})

	req, _ := http.NewRequest(http.MethodGet, "/api/insights/", http.NoBody)
	req.Header.Set("X-Auth", signToken(t, perm, key))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}

	var resp insightsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status.State != index.StateReady || resp.Usage == nil {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Usage.Size != 3520 || resp.Usage.Files != 3 {
		t.Fatalf("totals = %d bytes / %d files, want 3520 / 3", resp.Usage.Size, resp.Usage.Files)
	}
	sizes := map[string]int64{}
	for _, c := range resp.Usage.Children {
		sizes[c.Name] = c.Size
	}
	if sizes["media"] != 3500 || sizes["docs"] != 20 {
		t.Fatalf("children = %v", sizes)
	}

	// A subfolder.
	req, _ = http.NewRequest(http.MethodGet, "/api/insights/media", http.NoBody)
	req.Header.Set("X-Auth", signToken(t, perm, key))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Usage == nil || resp.Usage.Size != 3500 || len(resp.Usage.Children) != 2 {
		t.Fatalf("media = %+v", resp.Usage)
	}
}

func TestInsightsCannotLeaveScope(t *testing.T) {
	root := t.TempDir()
	scope := filepath.Join(root, "user")
	writeFile(t, root, "other/secret.bin", 999)
	writeFile(t, scope, "mine.txt", 1)
	useIndex(t, root)

	key := []byte("test-signing-key")
	perm := users.Permissions{}
	st := scopedUserStorage(t, scope, perm, key)
	handler := handle(insightsHandler, "/api/insights", st, &settings.Server{Root: root})

	for _, p := range []string{"/api/insights/..", "/api/insights/../other", "/api/insights/%2e%2e/other"} {
		req, _ := http.NewRequest(http.MethodGet, p, http.NoBody)
		req.Header.Set("X-Auth", signToken(t, perm, key))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "999") {
			t.Fatalf("%s leaked content outside the scope: %s", p, rec.Body.String())
		}
	}
}

func searchPaths(t *testing.T, handler http.Handler, token, query string) []string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "/api/search/?query="+query, http.NoBody)
	req.Header.Set("X-Auth", token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status = %d", rec.Code)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(rec.Body.String()), "\n") {
		if line == "" {
			continue
		}
		var hit struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(line), &hit); err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		out = append(out, hit.Path)
	}
	sort.Strings(out)
	return out
}

func TestSearchUsesIndexAndSkipsDeletedFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/report-2024.pdf", 10)
	writeFile(t, root, "b/report-2025.pdf", 10)
	writeFile(t, root, "b/notes.txt", 10)
	useIndex(t, root)

	// Changes after the index was built.
	writeFile(t, root, "c/report-late.pdf", 10)
	if err := os.Remove(filepath.Join(root, "a", "report-2024.pdf")); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{}
	st := scopedUserStorage(t, root, perm, key)
	handler := handle(searchHandler, "/api/search", st, &settings.Server{Root: root})

	got := searchPaths(t, handler, signToken(t, perm, key), "report")
	// From the index: the late file is not indexed yet, and the deleted one
	// is dropped by the existence check.
	if strings.Join(got, ",") != "b/report-2025.pdf" {
		t.Fatalf("hits = %v, want [b/report-2025.pdf]", got)
	}

	got = searchPaths(t, handler, signToken(t, perm, key), "type:txt")
	if strings.Join(got, ",") != "b/notes.txt" {
		t.Fatalf("type:txt hits = %v", got)
	}
}

func TestSearchFallsBackToDiskWithoutIndex(t *testing.T) {
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
	writeFile(t, root, "x/findme.txt", 1)

	key := []byte("test-signing-key")
	perm := users.Permissions{}
	st := scopedUserStorage(t, root, perm, key)
	handler := handle(searchHandler, "/api/search", st, &settings.Server{Root: root})

	if got := searchPaths(t, handler, signToken(t, perm, key), "findme"); strings.Join(got, ",") != "x/findme.txt" {
		t.Fatalf("hits = %v", got)
	}
}
