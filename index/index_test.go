package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, root, rel string, size int) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tree:
//
//	a/one.txt     100
//	a/b/two.log   250
//	a/b/Three.TXT 50
//	c/four.iso    1000
//	top.txt       7
func newTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "a/one.txt", 100)
	write(t, root, "a/b/two.log", 250)
	write(t, root, "a/b/Three.TXT", 50)
	write(t, root, "c/four.iso", 1000)
	write(t, root, "top.txt", 7)
	return root
}

func built(t *testing.T, root string) *Index {
	t.Helper()
	ix := New(root, time.Hour)
	if err := ix.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return ix
}

func names(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Path)
	}
	sort.Strings(out)
	return out
}

func TestUsageTotals(t *testing.T) {
	root := newTree(t)
	ix := built(t, root)

	u, err := ix.Usage(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.Size != 1407 || u.Files != 5 {
		t.Fatalf("root totals = %d bytes, %d files; want 1407, 5", u.Size, u.Files)
	}

	got := map[string]Item{}
	for _, c := range u.Children {
		got[c.Name] = c
	}
	if a := got["a"]; !a.IsDir || a.Size != 400 || a.Files != 3 {
		t.Fatalf("a = %+v, want dir 400 bytes 3 files", a)
	}
	if c := got["c"]; c.Size != 1000 || c.Files != 1 {
		t.Fatalf("c = %+v", c)
	}
	if f := got["top.txt"]; f.IsDir || f.Size != 7 {
		t.Fatalf("top.txt = %+v", f)
	}

	sub, err := ix.Usage(context.Background(), filepath.Join(root, "a", "b"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Size != 300 || len(sub.Children) != 2 {
		t.Fatalf("a/b = %+v", sub)
	}
}

func TestUsageFilteredTotalsHideExcludedContent(t *testing.T) {
	root := newTree(t)
	ix := built(t, root)

	// Hide everything under a/b, as a rule would.
	allowed := func(rel string) bool { return !strings.HasPrefix(rel, "/a/b") }
	u, err := ix.Usage(context.Background(), root, allowed)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range u.Children {
		if c.Name == "a" && (c.Size != 100 || c.Files != 1) {
			t.Fatalf("a should only count one.txt, got %+v", c)
		}
	}
	if u.Size != 1107 {
		t.Fatalf("root size = %d, want 1107 without a/b", u.Size)
	}
}

func TestSearch(t *testing.T) {
	root := newTree(t)
	ix := built(t, root)

	contains := func(term string) Match {
		return func(_, name string) bool { return strings.Contains(strings.ToLower(name), term) }
	}

	items, err := ix.Search(context.Background(), root, contains("txt"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(items), []string{"a/b/Three.TXT", "a/one.txt", "top.txt"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hits = %v, want %v", got, want)
	}

	// Scoped to a folder: paths are relative to it.
	items, err = ix.Search(context.Background(), filepath.Join(root, "a"), contains("txt"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(items); strings.Join(got, ",") != "b/Three.TXT,one.txt" {
		t.Fatalf("scoped hits = %v", got)
	}

	// Rules filter hits.
	items, err = ix.Search(context.Background(), root, contains("txt"), func(rel string) bool { return !strings.HasPrefix(rel, "/a") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(items); strings.Join(got, ",") != "top.txt" {
		t.Fatalf("filtered hits = %v", got)
	}

	// Limit.
	items, _ = ix.Search(context.Background(), root, func(string, string) bool { return true }, nil, 2)
	if len(items) != 2 {
		t.Fatalf("limit: got %d hits", len(items))
	}
}

func TestPathsOutsideRootAreNotFound(t *testing.T) {
	root := newTree(t)
	ix := built(t, root)

	for _, p := range []string{filepath.Dir(root), filepath.Join(root, ".."), filepath.Join(root, "missing"), filepath.Join(root, "top.txt")} {
		if _, err := ix.Usage(context.Background(), p, nil); !errors.Is(err, ErrNotReady) {
			t.Errorf("Usage(%q) err = %v, want ErrNotReady", p, err)
		}
	}
}

func TestSymlinksAreNotFollowed(t *testing.T) {
	root := newTree(t)
	outside := t.TempDir()
	write(t, outside, "secret/huge.bin", 5000)
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	ix := built(t, root)

	u, err := ix.Usage(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.Size != 1407 {
		t.Fatalf("root size = %d: symlinked content was counted", u.Size)
	}
	items, _ := ix.Search(context.Background(), root, func(_, name string) bool { return name == "huge.bin" }, nil, 0)
	if len(items) != 0 {
		t.Fatalf("index followed a symlink out of the root: %v", names(items))
	}
}

func TestSameSize(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x/a.bin", 4096)
	write(t, root, "y/b.bin", 4096)
	write(t, root, "y/c.bin", 4097)
	write(t, root, "tiny1", 10)
	write(t, root, "tiny2", 10)
	ix := built(t, root)

	groups, err := ix.SameSize(context.Background(), root, 1024, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %v, want one group", groups)
	}
	sort.Strings(groups[0])
	if strings.Join(groups[0], ",") != "/x/a.bin,/y/b.bin" {
		t.Fatalf("group = %v", groups[0])
	}
}

func TestTooLarge(t *testing.T) {
	root := newTree(t)
	ix := New(root, time.Hour)
	ix.maxEntries = 3
	ix.rebuild(context.Background())

	if st := ix.Status(); st.State != StateTooLarge {
		t.Fatalf("state = %q, want too_large", st.State)
	}
	if _, err := ix.Usage(context.Background(), root, nil); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Usage err = %v, want ErrNotReady", err)
	}
}

func TestDisabled(t *testing.T) {
	ix := New(t.TempDir(), 0)
	ix.Start(context.Background())
	ix.Refresh()
	if st := ix.Status(); st.State != StateDisabled {
		t.Fatalf("state = %q, want disabled", st.State)
	}
}

func TestStartBuildsInBackground(t *testing.T) {
	root := newTree(t)
	ix := New(root, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ix.Start(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for ix.Status().State != StateReady {
		if time.Now().After(deadline) {
			t.Fatal("index never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st := ix.Status(); st.Entries != 9 {
		t.Fatalf("entries = %d, want 9 (root, 3 folders, 5 files)", st.Entries)
	}
}
