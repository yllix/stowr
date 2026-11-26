package fbhttp

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/mux"

	fberrors "github.com/yllix/stowr/v3/errors"
	"github.com/yllix/stowr/v3/jobs"
)

const (
	// jobWorkers is how many copy/move jobs run at the same time. Disk
	// throughput, not CPU, is the limit, so a small number is best.
	jobWorkers = 2
	// jobQueueSize is how many jobs may wait for a free worker.
	jobQueueSize = 256
)

var (
	jobManagerOnce sync.Once
	jobManagerInst *jobs.Manager
)

// jobManager returns the process-wide job manager, starting it on first use.
func jobManager() *jobs.Manager {
	jobManagerOnce.Do(func() {
		jobManagerInst = jobs.NewManager(jobWorkers, jobQueueSize)
	})
	return jobManagerInst
}

// submitPatchJob queues a copy or rename (move) that has already passed the
// request's authorization and conflict checks, and answers 202 with the job.
// override reports whether the destination may already have existed, in which
// case a cancelled job must not delete it.
func submitPatchJob(w http.ResponseWriter, action, src, dst string, override bool, d *data, fileCache FileCache) (int, error) {
	// Check the permission up front so the client gets a 403 straight away
	// rather than a job that fails.
	switch action {
	case "copy":
		if !d.user.Perm.Create {
			return http.StatusForbidden, nil
		}
	case "rename":
		if !d.user.Perm.Rename {
			return http.StatusForbidden, nil
		}
	default:
		return http.StatusBadRequest, fberrors.ErrInvalidRequestParams
	}

	job, err := jobManager().Submit(d.user.ID, action, src, dst, func(ctx context.Context, j *jobs.Job) error {
		err := d.RunHook(func() error {
			return patchAction(ctx, action, src, dst, d, fileCache, j)
		}, action, src, dst, d.user)

		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			// The copy (or a move's cross-volume copy fallback) stopped part
			// way; the source is untouched. Remove the partial destination,
			// except when overriding: then it may hold the user's earlier data.
			// Only reached when the operation itself returned the
			// cancellation, never after a completed one.
			if !override {
				if rmErr := d.user.Fs.RemoveAll(dst); rmErr != nil {
					log.Printf("job %s: cleanup of %s after cancel failed: %v", action, dst, rmErr)
				}
			}
			return err
		}

		if err != nil {
			log.Printf("job %s %s -> %s failed: %v", action, src, dst, err)
			// A failed copy leaves a partial tree behind; remove it so the
			// user is not left with an incomplete copy that looks complete.
			// Never for moves: a move's cross-volume fallback may already
			// have deleted part of the source, so the destination may hold
			// the only copy of those files.
			if action == "copy" && !override {
				if rmErr := d.user.Fs.RemoveAll(dst); rmErr != nil {
					log.Printf("job %s: cleanup of %s after failure failed: %v", action, dst, rmErr)
				}
			}
			return publicJobError(err)
		}
		return nil
	})
	if errors.Is(err, jobs.ErrQueueFull) {
		return http.StatusServiceUnavailable, err
	}
	if err != nil {
		return http.StatusInternalServerError, err
	}

	return renderJSONStatus(w, http.StatusAccepted, job)
}

// publicJobError turns an operation error into a message that is safe to show
// to the user. Raw errors can contain absolute server paths, so they are only
// logged.
func publicJobError(err error) error {
	switch errToStatus(err) {
	case http.StatusForbidden:
		return errors.New("permission denied")
	case http.StatusNotFound:
		return errors.New("not found")
	case http.StatusConflict:
		return errors.New("already exists")
	default:
		return errors.New("operation failed")
	}
}

func renderJSONStatus(w http.ResponseWriter, status int, v interface{}) (int, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		return 0, err
	}
	return 0, nil
}

func jobErrToStatus(err error) int {
	if errors.Is(err, jobs.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

var jobsListHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	return renderJSON(w, r, jobManager().List(d.user.ID))
})

var jobGetHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	job, err := jobManager().Get(d.user.ID, mux.Vars(r)["id"])
	if err != nil {
		return jobErrToStatus(err), nil
	}
	return renderJSON(w, r, job)
})

var jobCancelHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	job, err := jobManager().Cancel(d.user.ID, mux.Vars(r)["id"])
	if err != nil {
		return jobErrToStatus(err), nil
	}
	return renderJSON(w, r, job)
})
