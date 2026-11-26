package fbhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"sync"
	"time"

	"github.com/yllix/stowr/v3/index"
	"github.com/yllix/stowr/v3/search"
)

const searchPingInterval = 5

var searchHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	response := make(chan map[string]interface{})
	ctx, cancel := context.WithCancelCause(r.Context())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Avoid connection timeout
		timeout := time.NewTimer(searchPingInterval * time.Second)
		defer timeout.Stop()
		for {
			var err error
			var infoBytes []byte
			select {
			case info := <-response:
				if info == nil {
					return
				}
				infoBytes, err = json.Marshal(info)
			case <-timeout.C:
				// Send a heartbeat packet
				infoBytes = nil
			case <-ctx.Done():
				return
			}
			if err != nil {
				cancel(err)
				return
			}
			_, err = w.Write(infoBytes)
			if err == nil {
				_, err = w.Write([]byte("\n"))
			}
			if err != nil {
				cancel(err)
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}()
	query := r.URL.Query().Get("query")

	// Answer from the file index when it covers this folder; otherwise walk
	// the disk as before.
	used, err := searchIndex(ctx, d, r.URL.Path, query, func(p string, dir bool) error {
		select {
		case <-ctx.Done():
		case response <- map[string]interface{}{
			"dir":  dir,
			"path": p,
		}:
		}
		return context.Cause(ctx)
	})
	if !used {
		err = search.Search(ctx, d.user.Fs, r.URL.Path, query, d, func(p string, f os.FileInfo) error {
			select {
			case <-ctx.Done():
			case response <- map[string]interface{}{
				"dir":  f.IsDir(),
				"path": p,
			}:
			}
			return context.Cause(ctx)
		})
	}
	close(response)
	wg.Wait()
	if err == nil {
		err = context.Cause(ctx)
	}
	// ignore cancellation errors from user aborts
	if err != nil && !errors.Is(err, context.Canceled) {
		return http.StatusInternalServerError, err
	}

	return 0, nil
})

// searchIndexLimit caps index search results; the disk walk had no cap but
// streamed slowly enough that this was never reached in practice.
const searchIndexLimit = 5000

// searchIndex runs a search against the file index. used is false when the
// index cannot answer (disabled, still building, or the folder is newer than
// the last build), in which case the caller walks the disk instead.
func searchIndex(ctx context.Context, d *data, scope, query string, found func(p string, dir bool) error) (used bool, err error) {
	ix := currentIndex()
	if ix == nil || ix.Status().State != index.StateReady {
		return false, nil
	}

	scope = path.Join("/", scope)
	abs, ok := indexPath(d, scope)
	if !ok {
		return false, nil
	}

	match := search.Matcher(query)
	items, err := ix.Search(ctx, abs,
		func(rel, _ string) bool { return match(path.Join(scope, rel)) },
		func(rel string) bool { return d.Check(path.Join(scope, rel)) },
		searchIndexLimit)
	if errors.Is(err, index.ErrNotReady) {
		return false, nil
	}
	if err != nil {
		return true, err
	}

	for _, it := range items {
		// The index can be up to a rebuild interval old: skip hits that
		// have since been deleted.
		if _, statErr := d.user.Fs.Stat(path.Join(scope, it.Path)); statErr != nil {
			continue
		}
		if err := found(it.Path, it.IsDir); err != nil {
			return true, err
		}
	}
	return true, nil
}
