package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	readyOK      = "ready"
	readyRunning = "running"
	readyTimeout = 2 * time.Minute
)

func (c Client) Ping(ctx context.Context) error {
	_, err := c.Call(ctx, Request{Op: OpPing})
	return err
}

func Up(ctx context.Context, layout Layout, program Program) (bool, error) {
	err := NewClient(layout).Ping(ctx)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, ErrNotRunning) {
		return false, err
	}
	return spawn(layout, program)
}

func spawn(layout Layout, program Program) (bool, error) {
	if err := os.MkdirAll(layout.State, 0o700); err != nil {
		return false, fmt.Errorf("creating %s: %w", layout.State, err)
	}
	stderr, err := os.OpenFile(layout.Stderr(), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return false, fmt.Errorf("opening %s: %w", layout.Stderr(), err)
	}
	defer stderr.Close()
	r, w, err := os.Pipe()
	if err != nil {
		return false, fmt.Errorf("creating the readiness pipe: %w", err)
	}
	defer r.Close()
	args := []string{program.Executable, "service", "supervise", "--ready-fd", "3"}
	for _, dir := range program.ExecPath {
		args = append(args, "--exec-path", dir)
	}
	cmd := &exec.Cmd{
		Path:        program.Executable,
		Args:        args,
		Dir:         layout.Host.Home,
		Stderr:      stderr,
		ExtraFiles:  []*os.File{w},
		SysProcAttr: &syscall.SysProcAttr{Setsid: true},
	}
	startErr := cmd.Start()
	w.Close()
	if startErr != nil {
		return false, fmt.Errorf("starting the supervisor: %w", startErr)
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return false, fmt.Errorf("detaching the supervisor (pid %d): %w", pid, err)
	}
	if err := r.SetReadDeadline(time.Now().Add(readyTimeout)); err != nil {
		return false, fmt.Errorf("waiting for the supervisor: %w", err)
	}
	msg, err := io.ReadAll(r)
	if err != nil {
		return false, fmt.Errorf("waiting for the supervisor (pid %d) to become ready: %w", pid, err)
	}
	switch reply := strings.TrimSpace(string(msg)); reply {
	case readyOK:
		return true, nil
	case readyRunning:
		return false, nil
	case "":
		return false, fmt.Errorf("the supervisor (pid %d) exited during startup; see %s", pid, layout.Stderr())
	default:
		return false, fmt.Errorf("the supervisor failed to start: %s", strings.TrimPrefix(reply, "error: "))
	}
}

func ReadyReporter(f *os.File) func(error) {
	return func(err error) {
		reply := readyOK
		switch {
		case errors.Is(err, ErrAlreadyRunning):
			reply = readyRunning
		case err != nil:
			reply = "error: " + strings.ReplaceAll(err.Error(), "\n", " ")
		}
		fmt.Fprintln(f, reply)
		f.Close()
	}
}
