package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func Replace(path string, perm fs.FileMode, fill func(io.Writer) error) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".new-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if removeErr := os.Remove(tmp.Name()); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("removing %s: %w", tmp.Name(), removeErr))
		}
	}()
	fillErr := fill(tmp)
	if fillErr == nil {
		fillErr = tmp.Chmod(perm)
	}
	closeErr := tmp.Close()
	if fillErr != nil {
		return fmt.Errorf("writing %s: %w", path, fillErr)
	}
	if closeErr != nil {
		return fmt.Errorf("writing %s: %w", path, closeErr)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	committed = true
	return nil
}

func WriteKeepingMode(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return Replace(path, info.Mode().Perm(), func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}
