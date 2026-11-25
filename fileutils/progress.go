package fileutils

import (
	"context"
	"io"
	"io/fs"

	"github.com/spf13/afero"
)

// copyBufferSize is the read size used when copying file contents. It is
// larger than io.Copy's 32 KiB default, which helps on spinning disks and
// network storage, and bounds how much is read between cancellation checks.
const copyBufferSize = 256 << 10

// Progress receives progress updates from CopyContext and MoveFileContext.
// The copy functions call it from a single goroutine.
type Progress interface {
	// SetTotals reports the size of the tree about to be copied.
	SetTotals(bytes int64, files int)
	// AddBytes reports n more bytes copied.
	AddBytes(n int64)
	// FileDone reports one more file fully copied.
	FileDone()
}

// progressReader reports bytes read to p and stops with ctx.Err() once ctx is
// done, so a large single-file copy can be cancelled part-way through.
type progressReader struct {
	ctx context.Context
	r   io.Reader
	p   Progress
}

func (pr *progressReader) Read(b []byte) (int, error) {
	if err := pr.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := pr.r.Read(b)
	if n > 0 && pr.p != nil {
		pr.p.AddBytes(int64(n))
	}
	return n, err
}

// TreeSize returns the total size in bytes and the number of regular files
// under root (root itself when it is a file). Entries that cannot be read are
// skipped: the copy that follows reports those errors itself.
func TreeSize(ctx context.Context, afs afero.Fs, root string) (int64, int, error) {
	var bytes int64
	var files int

	err := afero.Walk(afs, root, func(_ string, info fs.FileInfo, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil || info == nil {
			return nil
		}
		if info.Mode().IsRegular() {
			bytes += info.Size()
			files++
		}
		return nil
	})

	return bytes, files, err
}
