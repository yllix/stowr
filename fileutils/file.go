package fileutils

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/spf13/afero"
)

// MoveFile moves file from src to dst.
// By default the rename filesystem system call is used. If src and dst point to different volumes
// the file copy is used as a fallback
func MoveFile(afs afero.Fs, src, dst string, fileMode, dirMode fs.FileMode) error {
	return MoveFileContext(context.Background(), afs, src, dst, fileMode, dirMode, nil)
}

// MoveFileContext is MoveFile with cancellation and progress reporting for
// the copy fallback. A same-volume rename is atomic and reports no progress.
func MoveFileContext(ctx context.Context, afs afero.Fs, src, dst string, fileMode, dirMode fs.FileMode, p Progress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if afs.Rename(src, dst) == nil {
		return nil
	}
	// fallback
	err := CopyContext(ctx, afs, src, dst, fileMode, dirMode, p)
	if err != nil {
		_ = afs.Remove(dst)
		return err
	}
	if err := afs.RemoveAll(src); err != nil {
		return err
	}
	return nil
}

// CopyFile copies a file from source to dest and returns
// an error if any.
func CopyFile(afs afero.Fs, source, dest string, fileMode, dirMode fs.FileMode) error {
	return copyFile(context.Background(), afs, source, dest, fileMode, dirMode, nil)
}

func copyFile(ctx context.Context, afs afero.Fs, source, dest string, fileMode, dirMode fs.FileMode, p Progress) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Open the source file.
	src, err := afs.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()

	// Makes the directory needed to create the dst
	// file.
	err = afs.MkdirAll(filepath.Dir(dest), dirMode)
	if err != nil {
		return err
	}

	// Create the destination file.
	dst, err := afs.OpenFile(dest, os.O_RDWR|os.O_CREATE|os.O_TRUNC, fileMode)
	if err != nil {
		return err
	}
	defer dst.Close()

	// Copy the contents of the file.
	_, err = io.CopyBuffer(dst, &progressReader{ctx: ctx, r: src, p: p}, make([]byte, copyBufferSize))
	if err != nil {
		return err
	}

	// Copy the mode
	info, err := afs.Stat(source)
	if err != nil {
		return err
	}
	err = afs.Chmod(dest, info.Mode())
	if err != nil && !chmodNotAllowed(err) {
		return err
	}

	if p != nil {
		p.FileDone()
	}

	return nil
}

// CommonPrefix returns common directory path of provided files
func CommonPrefix(sep byte, paths ...string) string {
	// Handle special cases.
	switch len(paths) {
	case 0:
		return ""
	case 1:
		return path.Clean(paths[0])
	}

	// Note, we treat string as []byte, not []rune as is often
	// done in Go. (And sep as byte, not rune). This is because
	// most/all supported OS' treat paths as string of non-zero
	// bytes. A filename may be displayed as a sequence of Unicode
	// runes (typically encoded as UTF-8) but paths are
	// not required to be valid UTF-8 or in any normalized form
	// (e.g. "é" (U+00C9) and "é" (U+0065,U+0301) are different
	// file names.
	c := []byte(path.Clean(paths[0]))

	// We add a trailing sep to handle the case where the
	// common prefix directory is included in the path list
	// (e.g. /home/user1, /home/user1/foo, /home/user1/bar).
	// path.Clean will have cleaned off trailing / separators with
	// the exception of the root directory, "/" (in which case we
	// make it "//", but this will get fixed up to "/" below).
	c = append(c, sep)

	// Ignore the first path since it's already in c
	for _, v := range paths[1:] {
		// Clean up each path before testing it
		v = path.Clean(v) + string(sep)

		// Find the first non-common byte and truncate c
		if len(v) < len(c) {
			c = c[:len(v)]
		}
		for i := 0; i < len(c); i++ {
			if v[i] != c[i] {
				c = c[:i]
				break
			}
		}
	}

	// Remove trailing non-separator characters and the final separator
	for i := len(c) - 1; i >= 0; i-- {
		if c[i] == sep {
			c = c[:i]
			break
		}
	}

	return string(c)
}

// chmodNotAllowed reports whether a chmod failed because the filesystem does
// not allow changing mode bits, rather than because something went wrong.
// ZFS datasets with NFSv4 ACLs and aclmode=restricted (the TrueNAS default for
// SMB shares) refuse every chmod with EPERM; there the inherited ACL decides
// the new file's permissions, so the copy is still complete and correct.
func chmodNotAllowed(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, errors.ErrUnsupported)
}
