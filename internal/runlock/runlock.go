package runlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const fileName = "run.lock"

type HeldError struct {
	Path  string
	Owner string
}

func (e *HeldError) Error() string {
	if e.Owner == "" {
		return "another phix run holds " + e.Path
	}
	return fmt.Sprintf("another phix run (pid %s) holds %s", e.Owner, e.Path)
}

type Lock struct{ f *os.File }

func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating the lock directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, fileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		owner, _ := os.ReadFile(path)
		closeErr := f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(&HeldError{Path: path, Owner: strings.TrimSpace(string(owner))}, closeErr)
		}
		return nil, errors.Join(fmt.Errorf("locking %s: %w", path, err), closeErr)
	}
	if err := f.Truncate(0); err != nil {
		return nil, errors.Join(fmt.Errorf("recording the owner in %s: %w", path, err), f.Close())
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		return nil, errors.Join(fmt.Errorf("recording the owner in %s: %w", path, err), f.Close())
	}
	return &Lock{f: f}, nil
}

func (l *Lock) Release() error {
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("releasing %s: %w", l.f.Name(), err)
	}
	return nil
}
