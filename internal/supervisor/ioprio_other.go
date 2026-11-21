//go:build !linux

package supervisor

import "errors"

const (
	ioBestEffort      = 2
	ioDefaultPriority = 4
)

func setIOPriority(class, _ int) error {
	if class == 0 {
		return nil
	}
	return errors.New("IO scheduling classes are only supported on Linux")
}
