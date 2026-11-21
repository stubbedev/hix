package supervisor

import (
	"fmt"
	"os"
	"syscall"
)

type ExecSpec struct {
	Nice       *int
	IOClass    *int
	IOPriority *int
	UMask      *int
}

func Exec(spec ExecSpec, path string, argv []string) error {
	if spec.UMask != nil {
		syscall.Umask(*spec.UMask)
	}
	if spec.Nice != nil {
		if err := syscall.Setpriority(syscall.PRIO_PROCESS, 0, *spec.Nice); err != nil {
			return fmt.Errorf("setting nice %d: %w", *spec.Nice, err)
		}
	}
	if spec.IOClass != nil || spec.IOPriority != nil {
		class, prio := ioBestEffort, ioDefaultPriority
		if spec.IOClass != nil {
			class = *spec.IOClass
		}
		if spec.IOPriority != nil {
			prio = *spec.IOPriority
		}
		if err := setIOPriority(class, prio); err != nil {
			return err
		}
	}
	if err := syscall.Exec(path, argv, os.Environ()); err != nil { //nolint:gosec
		return fmt.Errorf("executing %s: %w", path, err)
	}
	return nil
}
