package fileutils

import (
	"context"
	"io/fs"
	"os"
	"path"

	"github.com/spf13/afero"
)

// Copy copies a file or folder from one place to another.
func Copy(afs afero.Fs, src, dst string, fileMode, dirMode fs.FileMode) error {
	return CopyContext(context.Background(), afs, src, dst, fileMode, dirMode, nil)
}

// CopyContext is Copy with cancellation and progress reporting. It stops
// between files and between reads once ctx is done, returning ctx.Err(). When
// p is not nil, the size of the source tree is measured first and reported
// through p.SetTotals, then progress is reported as data is copied.
func CopyContext(ctx context.Context, afs afero.Fs, src, dst string, fileMode, dirMode fs.FileMode, p Progress) error {
	if src = path.Clean("/" + src); src == "" {
		return os.ErrNotExist
	}

	if dst = path.Clean("/" + dst); dst == "" {
		return os.ErrNotExist
	}

	if src == "/" || dst == "/" {
		// Prohibit copying from or to the virtual root directory.
		return os.ErrInvalid
	}

	if dst == src {
		return os.ErrInvalid
	}

	info, err := afs.Stat(src)
	if err != nil {
		return err
	}

	if p != nil {
		bytes, files, err := TreeSize(ctx, afs, src)
		if err != nil {
			return err
		}
		p.SetTotals(bytes, files)
	}

	if info.IsDir() {
		return copyDir(ctx, afs, src, dst, fileMode, dirMode, p)
	}

	return copyFile(ctx, afs, src, dst, fileMode, dirMode, p)
}
