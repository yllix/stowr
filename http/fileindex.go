package fbhttp

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yllix/stowr/v3/files"
	"github.com/yllix/stowr/v3/index"
)

// defaultIndexInterval is how often the file index is rebuilt. Changes made
// through Stowr also trigger a rebuild (at most every couple of minutes).
const defaultIndexInterval = time.Hour

var (
	fileIndexMu sync.Mutex
	fileIndex   *index.Index
)

// startFileIndex begins indexing root in the background. The interval can be
// changed with STOWR_INDEX_INTERVAL (a Go duration such as "30m"); "0" or
// "off" disables the index, and search falls back to walking the disk.
func startFileIndex(root string) {
	interval := defaultIndexInterval
	if v := strings.TrimSpace(os.Getenv("STOWR_INDEX_INTERVAL")); v != "" {
		switch d, err := time.ParseDuration(v); {
		case v == "off" || v == "0":
			interval = 0
		case err != nil:
			log.Printf("index: ignoring invalid STOWR_INDEX_INTERVAL %q: %v", v, err)
		default:
			interval = d
		}
	}

	fileIndexMu.Lock()
	defer fileIndexMu.Unlock()
	if fileIndex != nil && fileIndex.Root() == filepath.Clean(root) {
		return
	}
	fileIndex = index.New(root, interval)
	fileIndex.Start(context.Background())
}

func currentIndex() *index.Index {
	fileIndexMu.Lock()
	defer fileIndexMu.Unlock()
	return fileIndex
}

// refreshIndex asks for a rebuild after files changed through Stowr.
func refreshIndex() {
	if ix := currentIndex(); ix != nil {
		ix.Refresh()
	}
}

// refreshIndexAfter wraps a handler that changes files and requests an index
// rebuild when it succeeds.
func refreshIndexAfter(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(sw, r)
		if sw.status < 400 {
			refreshIndex()
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// indexPath maps a path in the user's file view to the absolute on-disk path
// the index uses. RealPath also rejects paths that leave the user's scope.
func indexPath(d *data, p string) (string, bool) {
	base := files.BasePath(d.user.Fs)
	if base == nil {
		return "", false
	}
	real, err := base.RealPath(path.Join("/", p))
	if err != nil {
		return "", false
	}
	return real, true
}

// indexAllowed returns a filter applying the user's rules and hidden-file
// preference to paths relative to the folder base, or nil when nothing needs
// filtering (the fast path, which can use precomputed folder totals).
func indexAllowed(d *data, base string) index.Allowed {
	if len(d.settings.Rules) == 0 && len(d.user.Rules) == 0 && !d.user.HideDotfiles {
		return nil
	}
	return func(rel string) bool {
		return d.Check(path.Join(base, rel))
	}
}

type insightsResponse struct {
	Status index.Status `json:"status"`
	Path   string       `json:"path"`
	Usage  *index.Usage `json:"usage,omitempty"`
}

// insightsHandler serves GET /api/insights/<path>: the size of a folder and
// of each of its children, from the file index.
var insightsHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	ix := currentIndex()
	if ix == nil {
		return renderJSON(w, r, insightsResponse{Status: index.Status{State: index.StateDisabled}, Path: r.URL.Path})
	}

	p := path.Join("/", r.URL.Path)
	if !d.Check(p) {
		return http.StatusForbidden, nil
	}

	resp := insightsResponse{Status: ix.Status(), Path: p}
	if resp.Status.State != index.StateReady {
		return renderJSON(w, r, resp)
	}

	abs, ok := indexPath(d, p)
	if !ok {
		return http.StatusForbidden, nil
	}
	usage, err := ix.Usage(r.Context(), abs, indexAllowed(d, p))
	if errors.Is(err, index.ErrNotReady) {
		// Not in the index: created after the last build, or not a folder.
		return http.StatusNotFound, nil
	}
	if err != nil {
		return http.StatusInternalServerError, err
	}
	resp.Usage = &usage
	return renderJSON(w, r, resp)
})

// indexStatusHandler serves GET /api/index.
var indexStatusHandler = withUser(func(w http.ResponseWriter, r *http.Request, _ *data) (int, error) {
	ix := currentIndex()
	if ix == nil {
		return renderJSON(w, r, index.Status{State: index.StateDisabled})
	}
	return renderJSON(w, r, ix.Status())
})

// indexRefreshHandler serves POST /api/index/refresh (admins only).
var indexRefreshHandler = withAdmin(func(w http.ResponseWriter, r *http.Request, _ *data) (int, error) {
	refreshIndex()
	w.WriteHeader(http.StatusAccepted)
	return 0, nil
})
