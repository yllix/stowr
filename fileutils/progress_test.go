package fileutils

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/spf13/afero"
)

type recorder struct {
	mu         sync.Mutex
	totalBytes int64
	totalFiles int
	bytes      int64
	files      int
	onBytes    func(total int64)
}

func (r *recorder) SetTotals(b int64, f int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.totalBytes, r.totalFiles = b, f
}

func (r *recorder) AddBytes(n int64) {
	r.mu.Lock()
	r.bytes += n
	total := r.bytes
	cb := r.onBytes
	r.mu.Unlock()
	if cb != nil {
		cb(total)
	}
}

func (r *recorder) FileDone() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files++
}

func newTree(t *testing.T) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	if err := fs.MkdirAll("/src/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	for name, size := range map[string]int{"/src/a.bin": 1000, "/src/sub/b.bin": 2500, "/src/sub/c.bin": 0} {
		if err := afero.WriteFile(fs, name, bytes.Repeat([]byte("x"), size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return fs
}

func TestTreeSize(t *testing.T) {
	fs := newTree(t)

	b, f, err := TreeSize(context.Background(), fs, "/src")
	if err != nil {
		t.Fatal(err)
	}
	if b != 3500 || f != 3 {
		t.Fatalf("TreeSize = %d bytes, %d files; want 3500, 3", b, f)
	}

	b, f, err = TreeSize(context.Background(), fs, "/src/a.bin")
	if err != nil {
		t.Fatal(err)
	}
	if b != 1000 || f != 1 {
		t.Fatalf("TreeSize(file) = %d bytes, %d files; want 1000, 1", b, f)
	}
}

func TestCopyContextReportsProgress(t *testing.T) {
	fs := newTree(t)
	rec := &recorder{}

	if err := CopyContext(context.Background(), fs, "/src", "/dst", 0o644, 0o755, rec); err != nil {
		t.Fatalf("CopyContext: %v", err)
	}

	if rec.totalBytes != 3500 || rec.totalFiles != 3 {
		t.Fatalf("totals = %d/%d, want 3500/3", rec.totalBytes, rec.totalFiles)
	}
	if rec.bytes != 3500 || rec.files != 3 {
		t.Fatalf("progress = %d bytes, %d files; want 3500, 3", rec.bytes, rec.files)
	}

	got, err := afero.ReadFile(fs, "/dst/sub/b.bin")
	if err != nil || len(got) != 2500 {
		t.Fatalf("copied file: len=%d err=%v", len(got), err)
	}
}

func TestCopyContextWithoutProgressMatchesCopy(t *testing.T) {
	fs := newTree(t)
	if err := CopyContext(context.Background(), fs, "/src", "/dst", 0o644, 0o755, nil); err != nil {
		t.Fatalf("CopyContext: %v", err)
	}
	if _, err := fs.Stat("/dst/sub/c.bin"); err != nil {
		t.Fatalf("empty file not copied: %v", err)
	}
}

func TestCopyContextStopsWhenCanceled(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := fs.MkdirAll("/src", 0o755); err != nil {
		t.Fatal(err)
	}
	// Several files large enough to need many reads each.
	for _, name := range []string{"/src/1", "/src/2", "/src/3", "/src/4"} {
		if err := afero.WriteFile(fs, name, bytes.Repeat([]byte("x"), 1<<20), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &recorder{onBytes: func(total int64) {
		if total > 1<<19 {
			cancel()
		}
	}}

	err := CopyContext(ctx, fs, "/src", "/dst", 0o644, 0o755, rec)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rec.bytes >= 4<<20 {
		t.Fatalf("copy ran to completion (%d bytes) despite cancellation", rec.bytes)
	}
}

func TestCopyContextAlreadyCanceled(t *testing.T) {
	fs := newTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := CopyContext(ctx, fs, "/src", "/dst", 0o644, 0o755, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := fs.Stat("/dst"); err == nil {
		t.Fatal("destination created although the context was already canceled")
	}
}

func TestMoveFileContextRenameIsInstant(t *testing.T) {
	fs := newTree(t)
	rec := &recorder{}

	if err := MoveFileContext(context.Background(), fs, "/src", "/moved", 0o644, 0o755, rec); err != nil {
		t.Fatalf("MoveFileContext: %v", err)
	}
	if _, err := fs.Stat("/src"); err == nil {
		t.Fatal("source still exists after move")
	}
	if _, err := fs.Stat("/moved/sub/b.bin"); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
	if rec.bytes != 0 {
		t.Fatalf("rename should not copy data, reported %d bytes", rec.bytes)
	}
}
