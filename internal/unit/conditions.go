package unit

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func CheckConditions(conds []Condition, env Env, host Host) (bool, string) {
	var (
		anyTriggered, triggeredPassed bool
		firstTriggered                string
	)
	for _, c := range conds {
		ok := c.holds(env, host) != c.Negate
		if c.Triggered {
			if !anyTriggered {
				firstTriggered = c.describe()
			}
			anyTriggered = true
			triggeredPassed = triggeredPassed || ok
			continue
		}
		if !ok {
			return false, c.describe()
		}
	}
	if anyTriggered && !triggeredPassed {
		return false, firstTriggered + " (and every other triggering condition)"
	}
	return true, ""
}

func (c Condition) describe() string {
	neg := ""
	if c.Negate {
		neg = "!"
	}
	return fmt.Sprintf("%s=%s%s", c.Key, neg, c.Arg)
}

func (c Condition) holds(env Env, host Host) bool {
	switch c.Kind {
	case CondPathExists:
		_, err := os.Stat(c.Arg)
		return err == nil
	case CondPathExistsGlob:
		m, err := filepath.Glob(c.Arg)
		return err == nil && len(m) > 0
	case CondPathIsDirectory:
		info, err := os.Stat(c.Arg)
		return err == nil && info.IsDir()
	case CondPathIsSymbolicLink:
		info, err := os.Lstat(c.Arg)
		return err == nil && info.Mode()&os.ModeSymlink != 0
	case CondPathIsReadWrite:
		f, err := os.OpenFile(c.Arg, os.O_RDWR, 0)
		if err != nil {
			info, statErr := os.Stat(c.Arg)
			return statErr == nil && info.IsDir() && info.Mode().Perm()&0o200 != 0
		}
		f.Close()
		return true
	case CondFileNotEmpty:
		info, err := os.Stat(c.Arg)
		return err == nil && info.Mode().IsRegular() && info.Size() > 0
	case CondFileIsExecutable:
		info, err := os.Stat(c.Arg)
		return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
	case CondDirectoryNotEmpty:
		entries, err := os.ReadDir(c.Arg)
		return err == nil && len(entries) > 0
	case CondEnvironment:
		name, value, hasValue := strings.Cut(c.Arg, "=")
		got, set := env[name]
		if !hasValue {
			return set
		}
		return set && got == value
	case CondHost:
		ok, err := filepath.Match(c.Arg, host.Hostname)
		return err == nil && ok
	case CondUser:
		return c.Arg == host.User || c.Arg == strconv.Itoa(host.UID)
	}
	return false
}
