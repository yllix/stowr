// Package index keeps an in-memory index of the files under the server root:
// names, sizes and modification times, with per-folder totals. It powers
// fast filename search and the storage insights view without walking the
// disk on every request.
//
// The index is rebuilt in the background by walking the tree. Each build
// produces an immutable snapshot that is swapped in atomically, so readers
// never see a half-built index. Entries are stored in depth-first order, so
// every folder's subtree is one contiguous range of the entry slice.
package index

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// State describes whether the index can be used.
type State string

const (
	// StateBuilding means no snapshot exists yet.
	StateBuilding State = "building"
	// StateReady means a snapshot is available (a rebuild may be running).
	StateReady State = "ready"
	// StateTooLarge means the tree has more entries than the index allows.
	StateTooLarge State = "too_large"
	// StateDisabled means indexing is turned off.
	StateDisabled State = "disabled"
)

// ErrNotReady is returned when no usable snapshot exists.
var ErrNotReady = errors.New("file index is not ready")

// DefaultMaxEntries bounds memory use: at roughly 100–150 bytes per entry the
// index stays well under 1 GiB.
const DefaultMaxEntries = 5_000_000

var errTooLarge = errors.New("too many entries")

type entry struct {
	name   string
	lower  string // lower-cased name; empty when equal to name
	parent int32
	end    int32 // last entry index of this subtree (== own index for files)
	isDir  bool
	size   int64 // file size, or the total of the subtree for folders
	files  int64 // regular files in the subtree (1 for a file)
	mtime  int64 // unix seconds
}

func (e *entry) lowerName() string {
	if e.lower != "" {
		return e.lower
	}
	return e.name
}

type snapshot struct {
	root     string
	entries  []entry
	children map[int32][]int32
	built    time.Time
	took     time.Duration
}

// Status is a JSON-friendly summary of the index.
type Status struct {
	State    State     `json:"state"`
	Entries  int       `json:"entries"`
	BuiltAt  time.Time `json:"builtAt,omitzero"`
	Duration string    `json:"duration,omitempty"`
	Building bool      `json:"building"`
}

// Index is a background-maintained index of one directory tree.
type Index struct {
	root       string
	interval   time.Duration
	maxEntries int

	snap     atomic.Pointer[snapshot]
	state    atomic.Value // State
	building atomic.Bool

	mu        sync.Mutex
	requested bool
	trigger   chan struct{}
}

// New creates an index of root. It does nothing until Start is called.
// interval is how often to rebuild; zero disables indexing.
func New(root string, interval time.Duration) *Index {
	ix := &Index{
		root:       filepath.Clean(root),
		interval:   interval,
		maxEntries: DefaultMaxEntries,
		trigger:    make(chan struct{}, 1),
	}
	if interval <= 0 {
		ix.state.Store(StateDisabled)
	} else {
		ix.state.Store(StateBuilding)
	}
	return ix
}

// Root is the indexed directory.
func (ix *Index) Root() string { return ix.root }

// Start builds the index and keeps it fresh until ctx is done.
func (ix *Index) Start(ctx context.Context) {
	if ix.interval <= 0 {
		return
	}
	go ix.loop(ctx)
}

// Refresh asks for a rebuild soon, e.g. after files were changed through
// Stowr. Bursts of requests are coalesced into one rebuild, and rebuilds are
// at least minRebuildGap apart.
func (ix *Index) Refresh() {
	if ix.interval <= 0 {
		return
	}
	select {
	case ix.trigger <- struct{}{}:
	default:
	}
}

const minRebuildGap = 2 * time.Minute

func (ix *Index) loop(ctx context.Context) {
	ix.rebuild(ctx)
	timer := time.NewTimer(ix.interval)
	defer timer.Stop()
	last := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-ix.trigger:
			if wait := minRebuildGap - time.Since(last); wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
		}
		ix.rebuild(ctx)
		last = time.Now()
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(ix.interval)
	}
}

func (ix *Index) rebuild(ctx context.Context) {
	ix.building.Store(true)
	defer ix.building.Store(false)

	snap, err := build(ctx, ix.root, ix.maxEntries)
	switch {
	case errors.Is(err, errTooLarge):
		log.Printf("index: %s has more than %d entries; search falls back to walking the disk", ix.root, ix.maxEntries)
		ix.snap.Store(nil)
		ix.state.Store(StateTooLarge)
	case err != nil:
		if ctx.Err() == nil {
			log.Printf("index: build of %s failed: %v", ix.root, err)
		}
	default:
		ix.snap.Store(snap)
		ix.state.Store(StateReady)
		log.Printf("index: %d entries under %s in %s", len(snap.entries), ix.root, snap.took.Round(time.Millisecond))
	}
}

// Build replaces the snapshot synchronously. Intended for tests and tools.
func (ix *Index) Build(ctx context.Context) error {
	snap, err := build(ctx, ix.root, ix.maxEntries)
	if err != nil {
		return err
	}
	ix.snap.Store(snap)
	ix.state.Store(StateReady)
	return nil
}

// Status reports the index state.
func (ix *Index) Status() Status {
	st := Status{State: ix.state.Load().(State), Building: ix.building.Load()}
	if s := ix.snap.Load(); s != nil {
		st.Entries = len(s.entries)
		st.BuiltAt = s.built
		st.Duration = s.took.Round(time.Millisecond).String()
	}
	return st
}

func build(ctx context.Context, root string, maxEntries int) (*snapshot, error) {
	start := time.Now()
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("index root is not a directory")
	}

	s := &snapshot{root: root, children: make(map[int32][]int32)}
	s.entries = append(s.entries, entry{name: "", parent: -1, isDir: true, mtime: info.ModTime().Unix()})

	if err := walk(ctx, s, root, 0, maxEntries); err != nil {
		return nil, err
	}
	s.entries[0].end = int32(len(s.entries) - 1)
	s.built = time.Now()
	s.took = time.Since(start)
	return s, nil
}

// walk adds the contents of dir (already added as entry id) depth-first and
// fills in folder totals. Symlinks are recorded but never followed, so the
// index cannot escape the root.
func walk(ctx context.Context, s *snapshot, dir string, id int32, maxEntries int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dirents, err := os.ReadDir(dir)
	if err != nil {
		// Unreadable folders are kept as empty entries.
		return nil
	}

	for _, de := range dirents {
		if len(s.entries) >= maxEntries {
			return errTooLarge
		}

		info, err := de.Info()
		if err != nil {
			continue
		}

		name := de.Name()
		e := entry{name: name, parent: id, mtime: info.ModTime().Unix()}
		if lower := strings.ToLower(name); lower != name {
			e.lower = lower
		}

		child := int32(len(s.entries))
		s.children[id] = append(s.children[id], child)

		mode := info.Mode()
		switch {
		case mode.IsDir():
			e.isDir = true
			s.entries = append(s.entries, e)
			if err := walk(ctx, s, filepath.Join(dir, name), child, maxEntries); err != nil {
				return err
			}
			s.entries[child].end = int32(len(s.entries) - 1)
			s.entries[id].size += s.entries[child].size
			s.entries[id].files += s.entries[child].files
		case mode.IsRegular():
			e.size = info.Size()
			e.files = 1
			e.end = child
			s.entries = append(s.entries, e)
			s.entries[id].size += e.size
			s.entries[id].files++
		default:
			// Symlinks, devices, sockets: listed, no size, not followed.
			e.end = child
			s.entries = append(s.entries, e)
		}
	}

	return nil
}

func (ix *Index) ready() (*snapshot, error) {
	s := ix.snap.Load()
	if s == nil {
		return nil, ErrNotReady
	}
	return s, nil
}

// lookup finds the entry for an absolute path under the root.
func (s *snapshot) lookup(abs string) (int32, bool) {
	rel, err := filepath.Rel(s.root, filepath.Clean(abs))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return 0, false
	}
	if rel == "." {
		return 0, true
	}

	id := int32(0)
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if !s.entries[id].isDir {
			return 0, false
		}
		next := int32(-1)
		for _, c := range s.children[id] {
			if s.entries[c].name == part {
				next = c
				break
			}
		}
		if next < 0 {
			return 0, false
		}
		id = next
	}
	return id, true
}

// relPath returns the path of entry id relative to entry base, using "/".
func (s *snapshot) relPath(base, id int32) string {
	var parts []string
	for cur := id; cur != base && cur > 0; cur = s.entries[cur].parent {
		parts = append(parts, s.entries[cur].name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "/")
}

// Item is one search hit or folder child, with paths relative to the folder
// that was queried.
type Item struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	IsDir    bool      `json:"isDir"`
	Size     int64     `json:"size"`
	Files    int64     `json:"files"`
	Modified time.Time `json:"modified"`
}

func (s *snapshot) item(base, id int32) Item {
	e := &s.entries[id]
	return Item{
		Path:     s.relPath(base, id),
		Name:     e.name,
		IsDir:    e.isDir,
		Size:     e.size,
		Files:    e.files,
		Modified: time.Unix(e.mtime, 0),
	}
}

// Allowed reports whether a path (relative to the queried folder, "/"-separated,
// with a leading "/") may be shown to the user.
type Allowed func(relPath string) bool

// Match decides whether an entry is a search hit. relPath is relative to the
// searched folder with a leading "/"; name is the entry's own name.
type Match func(relPath, name string) bool

// Search returns entries under abs accepted by match and allowed, in
// depth-first order, stopping after limit hits (0 means no limit).
func (ix *Index) Search(ctx context.Context, abs string, match Match, allowed Allowed, limit int) ([]Item, error) {
	s, err := ix.ready()
	if err != nil {
		return nil, err
	}
	base, ok := s.lookup(abs)
	if !ok || !s.entries[base].isDir {
		return nil, ErrNotReady
	}

	var out []Item
	end := s.entries[base].end
	for id := base + 1; id <= end; id++ {
		if id&0x3fff == 0 {
			if err := ctx.Err(); err != nil {
				return out, err
			}
		}
		e := &s.entries[id]
		rel := "/" + s.relPath(base, id)
		if !match(rel, e.name) {
			continue
		}
		if allowed != nil && !allowed(rel) {
			continue
		}
		it := s.item(base, id)
		out = append(out, it)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Usage describes a folder and its immediate children.
type Usage struct {
	Size     int64     `json:"size"`
	Files    int64     `json:"files"`
	Children []Item    `json:"children"`
	BuiltAt  time.Time `json:"builtAt"`
}

// Usage returns the totals for the folder at abs and each of its children.
// When allowed is not nil, totals only count entries the user may see, so
// sizes never reveal hidden content.
func (ix *Index) Usage(ctx context.Context, abs string, allowed Allowed) (Usage, error) {
	s, err := ix.ready()
	if err != nil {
		return Usage{}, err
	}
	base, ok := s.lookup(abs)
	if !ok || !s.entries[base].isDir {
		return Usage{}, ErrNotReady
	}

	u := Usage{BuiltAt: s.built}
	for _, c := range s.children[base] {
		it := s.item(base, c)
		if allowed != nil {
			if !allowed("/" + it.Path) {
				continue
			}
			if it.IsDir {
				size, files, err := s.filteredTotals(ctx, base, c, allowed)
				if err != nil {
					return Usage{}, err
				}
				it.Size, it.Files = size, files
			}
		}
		u.Size += it.Size
		u.Files += it.Files
		u.Children = append(u.Children, it)
	}
	return u, nil
}

func (s *snapshot) filteredTotals(ctx context.Context, base, dir int32, allowed Allowed) (int64, int64, error) {
	var size, files int64
	end := s.entries[dir].end
	for id := dir + 1; id <= end; id++ {
		if id&0x3fff == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		e := &s.entries[id]
		if e.isDir || e.files == 0 {
			continue
		}
		if !allowed("/" + s.relPath(base, id)) {
			continue
		}
		size += e.size
		files++
	}
	return size, files, nil
}

// SameSize returns groups of regular files under abs that share a size of at
// least minSize, as paths relative to abs. Used to find duplicate candidates.
func (ix *Index) SameSize(ctx context.Context, abs string, minSize int64, allowed Allowed) ([][]string, error) {
	s, err := ix.ready()
	if err != nil {
		return nil, err
	}
	base, ok := s.lookup(abs)
	if !ok || !s.entries[base].isDir {
		return nil, ErrNotReady
	}

	bySize := make(map[int64][]int32)
	end := s.entries[base].end
	for id := base + 1; id <= end; id++ {
		e := &s.entries[id]
		if e.isDir || e.files != 1 || e.size < minSize {
			continue
		}
		bySize[e.size] = append(bySize[e.size], id)
	}

	var groups [][]string
	for _, ids := range bySize {
		if len(ids) < 2 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var paths []string
		for _, id := range ids {
			p := "/" + s.relPath(base, id)
			if allowed != nil && !allowed(p) {
				continue
			}
			paths = append(paths, p)
		}
		if len(paths) > 1 {
			groups = append(groups, paths)
		}
	}
	return groups, nil
}
