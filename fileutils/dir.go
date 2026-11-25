package fileutils

import (
	"context"
	"errors"
	"io/fs"

	"github.com/spf13/afero"
)

// CopyDir copies a directory from source to dest and all
// of its sub-directories. It doesn't stop if it finds an error
// during the copy. Returns an error if any.
func CopyDir(afs afero.Fs, source, dest string, fileMode, dirMode fs.FileMode) error {
	return copyDir(context.Background(), afs, source, dest, fileMode, dirMode, nil)
}

func copyDir(ctx context.Context, afs afero.Fs, source, dest string, fileMode, dirMode fs.FileMode, p Progress) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Get properties of source.
	srcinfo, err := afs.Stat(source)
	if err != nil {
		return err
	}

	// Create the destination directory.
	err = afs.MkdirAll(dest, srcinfo.Mode())
	if err != nil {
		return err
	}

	dir, err := afs.Open(source)
	if err != nil {
		return err
	}
	defer dir.Close()

	obs, err := dir.Readdir(-1)
	if err != nil {
		return err
	}

	var errs []error

	for _, obj := range obs {
		// Stop promptly on cancellation instead of collecting one
		// "context canceled" error per remaining entry.
		if err := ctx.Err(); err != nil {
			return err
		}

		fsource := source + "/" + obj.Name()
		fdest := dest + "/" + obj.Name()

		if obj.IsDir() {
			// Create sub-directories, recursively.
			err = copyDir(ctx, afs, fsource, fdest, fileMode, dirMode, p)
		} else {
			// Perform the file copy.
			err = copyFile(ctx, afs, fsource, fdest, fileMode, dirMode, p)
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			errs = append(errs, err)
		}
	}

	var errString string
	for _, err := range errs {
		errString += err.Error() + "\n"
	}

	if errString != "" {
		return errors.New(errString)
	}

	return nil
}
