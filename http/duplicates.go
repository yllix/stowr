package fbhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"

	"github.com/spf13/afero"

	"github.com/yllix/stowr/v3/index"
	"github.com/yllix/stowr/v3/jobs"
)

const (
	// defaultDuplicateMinSize skips small files, where duplicates waste
	// little space and are often intentional (icons, configs, licences).
	defaultDuplicateMinSize = 1 << 20
	// quickHashChunk is how much is read from each end of a file for the
	// first comparison pass.
	quickHashChunk = 64 << 10
	// maxDuplicateGroups caps the result size.
	maxDuplicateGroups = 5000
)

// DuplicateGroup is a set of files with identical content.
type DuplicateGroup struct {
	Size  int64    `json:"size"`
	Hash  string   `json:"hash"`
	Paths []string `json:"paths"`
}

// DuplicatesResult is the output of a duplicate scan.
type DuplicatesResult struct {
	Path      string           `json:"path"`
	MinSize   int64            `json:"minSize"`
	Groups    []DuplicateGroup `json:"groups"`
	Wasted    int64            `json:"wasted"`
	Truncated bool             `json:"truncated,omitempty"`
}

// duplicatesHandler serves POST /api/duplicates/<path>?minSize=<bytes>. It
// starts a background job that finds files with identical content under the
// folder and answers 202 with the job; the result is read with GET
// /api/jobs/{id} once the job is done.
var duplicatesHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	p := path.Join("/", r.URL.Path)
	if !d.Check(p) {
		return http.StatusForbidden, nil
	}

	minSize := int64(defaultDuplicateMinSize)
	if v := r.URL.Query().Get("minSize"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return http.StatusBadRequest, errors.New("minSize must be a positive number of bytes")
		}
		minSize = n
	}

	ix := currentIndex()
	if ix == nil || ix.Status().State != index.StateReady {
		// Candidates come from the index; without it a scan would have to
		// stat every file first.
		return http.StatusServiceUnavailable, errors.New("the file index is not ready yet")
	}
	abs, ok := indexPath(d, p)
	if !ok {
		return http.StatusForbidden, nil
	}
	allowed := indexAllowed(d, p)

	job, err := jobManager().Submit(d.user.ID, "duplicates", p, "", func(ctx context.Context, j *jobs.Job) error {
		groups, err := ix.SameSize(ctx, abs, minSize, allowed)
		if errors.Is(err, index.ErrNotReady) {
			return errors.New("this folder is not in the file index yet")
		}
		if err != nil {
			return err
		}

		res, err := findDuplicates(ctx, d.user.Fs, p, groups, j)
		if err != nil {
			return err
		}
		res.MinSize = minSize
		j.SetResult(res)
		return nil
	})
	if errors.Is(err, jobs.ErrQueueFull) {
		return http.StatusServiceUnavailable, err
	}
	if err != nil {
		return http.StatusInternalServerError, err
	}
	return renderJSONStatus(w, http.StatusAccepted, job)
})

type candidate struct {
	path string // in the user's view
	size int64
}

// findDuplicates compares files that share a size. Paths in groups are
// relative to base with a leading "/". Files are read through the user's
// filesystem, so scope and symlink protections apply. Each same-size group is
// split first by a hash of the file's first and last 64 KiB, and only files
// that still match are hashed in full.
func findDuplicates(ctx context.Context, afs afero.Fs, base string, groups [][]string, p *jobs.Job) (*DuplicatesResult, error) {
	res := &DuplicatesResult{Path: base, Groups: []DuplicateGroup{}}

	// Confirm sizes on disk (the index may be slightly stale) and work out
	// the total for progress reporting.
	var sets [][]candidate
	var totalBytes int64
	var totalFiles int
	for _, g := range groups {
		bySize := map[int64][]candidate{}
		for _, rel := range g {
			full := path.Join(base, rel)
			info, err := afs.Stat(full)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			bySize[info.Size()] = append(bySize[info.Size()], candidate{path: full, size: info.Size()})
		}
		for _, set := range bySize {
			if len(set) < 2 {
				continue
			}
			sets = append(sets, set)
			totalFiles += len(set)
			for _, c := range set {
				totalBytes += c.size
			}
		}
	}
	p.SetTotals(totalBytes, totalFiles)

	for _, set := range sets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// Pass 1: ends of the file.
		quick := map[string][]candidate{}
		for _, c := range set {
			h, err := quickHash(afs, c)
			if err != nil {
				p.AddBytes(c.size)
				p.FileDone()
				continue
			}
			quick[h] = append(quick[h], c)
		}

		// Pass 2: full content, only where the quick hash still matches.
		for _, same := range quick {
			if len(same) < 2 {
				for _, c := range same {
					p.AddBytes(c.size)
					p.FileDone()
				}
				continue
			}
			full := map[string][]string{}
			for _, c := range same {
				h, err := fullHash(ctx, afs, c, p)
				p.FileDone()
				if err != nil {
					if ctxErr := ctx.Err(); ctxErr != nil {
						return nil, ctxErr
					}
					continue
				}
				full[h] = append(full[h], c.path)
			}
			for h, paths := range full {
				if len(paths) < 2 {
					continue
				}
				sort.Strings(paths)
				size := same[0].size
				res.Groups = append(res.Groups, DuplicateGroup{Size: size, Hash: h, Paths: paths})
				res.Wasted += size * int64(len(paths)-1)
			}
		}
	}

	sort.Slice(res.Groups, func(a, b int) bool {
		wa := res.Groups[a].Size * int64(len(res.Groups[a].Paths)-1)
		wb := res.Groups[b].Size * int64(len(res.Groups[b].Paths)-1)
		if wa != wb {
			return wa > wb
		}
		return res.Groups[a].Paths[0] < res.Groups[b].Paths[0]
	})
	if len(res.Groups) > maxDuplicateGroups {
		res.Groups = res.Groups[:maxDuplicateGroups]
		res.Truncated = true
	}
	return res, nil
}

func quickHash(afs afero.Fs, c candidate) (string, error) {
	f, err := afs.Open(c.path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, quickHashChunk)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	_, _ = h.Write(buf[:n]) // hash writes never fail

	if c.size > 2*quickHashChunk {
		if _, err := f.Seek(-quickHashChunk, io.SeekEnd); err != nil {
			return "", err
		}
		n, err = io.ReadFull(f, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return "", err
		}
		_, _ = h.Write(buf[:n]) // hash writes never fail
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fullHash(ctx context.Context, afs afero.Fs, c candidate, p *jobs.Job) (string, error) {
	f, err := afs.Open(c.path)
	if err != nil {
		p.AddBytes(c.size)
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	r := &ctxProgressReader{ctx: ctx, r: f, p: p}
	if _, err := io.CopyBuffer(h, r, make([]byte, 256<<10)); err != nil {
		return "", err
	}
	if r.read != c.size {
		// The file changed while it was read; don't trust the hash.
		return "", errors.New("file changed during hashing")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type ctxProgressReader struct {
	ctx  context.Context
	r    io.Reader
	p    *jobs.Job
	read int64
}

func (cr *ctxProgressReader) Read(b []byte) (int, error) {
	if err := cr.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := cr.r.Read(b)
	if n > 0 {
		cr.read += int64(n)
		cr.p.AddBytes(int64(n))
	}
	return n, err
}
