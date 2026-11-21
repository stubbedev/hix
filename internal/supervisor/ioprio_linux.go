package supervisor

import (
	"fmt"

	"golang.org/x/sys/unix"
)

const (
	ioBestEffort      = 2
	ioDefaultPriority = 4
	ioprioWhoProcess  = 1
	ioprioClassShift  = 13
)

func setIOPriority(class, prio int) error {
	if class == 0 {
		return nil
	}
	value := class<<ioprioClassShift | prio
	if _, _, errno := unix.Syscall(unix.SYS_IOPRIO_SET, ioprioWhoProcess, 0, uintptr(value)); errno != 0 {
		return fmt.Errorf("setting IO scheduling class %d priority %d: %w", class, prio, errno)
	}
	return nil
}
