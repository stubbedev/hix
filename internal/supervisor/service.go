package supervisor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/stubbedev/phix/internal/proc"
	"github.com/stubbedev/phix/internal/unit"
)

const (
	goneProbeInterval = 100 * time.Millisecond
	finalKillWait     = 5 * time.Second
)

func controlSucceeded(es exitStatus) bool {
	return es.spawnErr == nil && !es.timedOut && es.signal == 0 && es.code == 0
}

func (m *Manager) serviceEnv(st *state) (unit.Env, error) {
	s := st.u.Service
	env := m.env.Clone()
	env.Apply(s.Environment)
	for _, f := range s.EnvironmentFiles {
		data, err := os.ReadFile(f.Path)
		if f.Optional && errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading EnvironmentFile %s: %w", f.Path, err)
		}
		vars, err := unit.ParseEnvironmentFile(data)
		if err != nil {
			return nil, fmt.Errorf("parsing EnvironmentFile %s: %w", f.Path, err)
		}
		env.Apply(vars)
	}
	for _, name := range s.UnsetEnvironment {
		delete(env, name)
	}
	env["INVOCATION_ID"] = strconv.FormatInt(m.now().UnixNano(), 16)
	return env, nil
}

func (m *Manager) makeDirectories(st *state) error {
	h := m.layout.Host
	d := st.u.Service.Directories
	for _, group := range []struct {
		base string
		dirs []string
		env  string
	}{
		{h.RuntimeDir, d.Runtime, "RUNTIME_DIRECTORY"},
		{h.StateHome, d.State, "STATE_DIRECTORY"},
		{h.CacheHome, d.Cache, "CACHE_DIRECTORY"},
		{filepath.Join(h.StateHome, "log"), d.Logs, "LOGS_DIRECTORY"},
		{h.ConfigHome, d.Configuration, "CONFIGURATION_DIRECTORY"},
	} {
		var paths string
		for _, rel := range group.dirs {
			path := filepath.Join(group.base, rel)
			if err := os.MkdirAll(path, 0o700); err != nil {
				return fmt.Errorf("creating %s: %w", path, err)
			}
			if paths != "" {
				paths += ":"
			}
			paths += path
		}
		if paths != "" {
			st.env[group.env] = paths
		}
	}
	return nil
}

func (m *Manager) startService(st *state) {
	s := st.u.Service
	env, err := m.serviceEnv(st)
	if err != nil {
		m.failActivation(st, exitStatus{spawnErr: err}, err)
		return
	}
	st.env, st.ready, st.mainExit = env, false, nil
	if err := m.makeDirectories(st); err != nil {
		m.failActivation(st, exitStatus{spawnErr: err}, err)
		return
	}
	if s.Type.Notifies() {
		n, err := m.openNotifier(st)
		if err != nil {
			m.failActivation(st, exitStatus{spawnErr: err}, err)
			return
		}
		st.notify = n
		st.env["NOTIFY_SOCKET"] = n.path
	}
	m.setState(st, stateActivating, "condition")
	m.arm(st, s.TimeoutStart, func() {
		m.ulog(st, "Start operation timed out after %s", s.TimeoutStart)
		m.failActivation(st, exitStatus{timedOut: true}, fmt.Errorf("%s: start timed out after %s", st.name(), s.TimeoutStart))
	})
	m.runSeq(st, s.ExecCondition, "ExecCondition", func(es exitStatus, failed bool) {
		switch {
		case failed && es.spawnErr == nil && es.signal == 0 && es.code >= 1 && es.code <= 254:
			m.disarm(st)
			m.ulog(st, "Skipped: ExecCondition %s", es)
			st.result = "condition"
			m.teardown(st, false, func() {
				m.finalize(st, "condition")
				m.finishJob(st.name(), nil)
			})
		case failed:
			m.failActivation(st, es, fmt.Errorf("%s: ExecCondition %s", st.name(), es))
		default:
			m.startPre(st)
		}
	})
}

func (m *Manager) startPre(st *state) {
	m.setState(st, stateActivating, "start-pre")
	m.runSeq(st, st.u.Service.ExecStartPre, "ExecStartPre", func(es exitStatus, failed bool) {
		if failed {
			m.failActivation(st, es, fmt.Errorf("%s: ExecStartPre %s", st.name(), es))
			return
		}
		m.startMain(st)
	})
}

func (m *Manager) startMain(st *state) {
	s := st.u.Service
	m.setState(st, stateActivating, "start")
	if s.Type == unit.Oneshot {
		m.runOneshot(st, s.ExecStart)
		return
	}
	p, err := m.spawn(st, "main", s.ExecStart[0])
	if err != nil {
		m.failActivation(st, exitStatus{spawnErr: err}, fmt.Errorf("%s: %w", st.name(), err))
		return
	}
	st.main = p
	if s.Type.Notifies() {
		return
	}
	m.startPost(st)
}

func (m *Manager) runOneshot(st *state, cmds []unit.Command) {
	if len(cmds) == 0 {
		m.startPost(st)
		return
	}
	c := cmds[0]
	p, err := m.spawn(st, "main", c)
	if err != nil {
		if c.IgnoreFailure {
			m.runOneshot(st, cmds[1:])
			return
		}
		m.failActivation(st, exitStatus{spawnErr: err}, fmt.Errorf("%s: %w", st.name(), err))
		return
	}
	st.main = p
	st.onMain = func(es exitStatus) {
		if !es.success(st.u.Service) && !c.IgnoreFailure {
			m.failActivation(st, es, fmt.Errorf("%s: %s", st.name(), es))
			return
		}
		m.runOneshot(st, cmds[1:])
	}
}

func (m *Manager) startPost(st *state) {
	m.setState(st, stateActivating, "start-post")
	m.runSeq(st, st.u.Service.ExecStartPost, "ExecStartPost", func(es exitStatus, failed bool) {
		if failed {
			m.failActivation(st, es, fmt.Errorf("%s: ExecStartPost %s", st.name(), es))
			return
		}
		m.becomeActive(st)
	})
}

func (m *Manager) becomeActive(st *state) {
	s := st.u.Service
	m.disarm(st)
	if s.Type == unit.Oneshot && !s.RemainAfterExit {
		m.ulog(st, "Finished")
		st.activeEnter = m.now()
		m.teardown(st, false, func() {
			m.finalize(st, "success")
			m.finishJob(st.name(), nil)
		})
		return
	}
	sub := "running"
	if st.main == nil {
		sub = "exited"
	}
	m.setState(st, stateActive, sub)
	m.ulog(st, "Started")
	m.unitActivated(st)
	m.finishJob(st.name(), nil)
	if exit := st.mainExit; exit != nil {
		st.mainExit = nil
		m.serviceEnded(st, *exit)
	}
}

func (m *Manager) failActivation(st *state, es exitStatus, err error) {
	s := st.u.Service
	m.disarm(st)
	st.onControl, st.onMain = nil, nil
	m.ulog(st, "Failed to start: %v", err)
	result := es.result()
	if result == "success" {
		result = "exit-code"
	}
	restart := !m.shuttingDown && shouldRestart(s, es)
	m.teardown(st, false, func() {
		if restart {
			m.scheduleRestart(st, result)
		} else {
			m.finalize(st, result)
		}
		m.finishJob(st.name(), err)
	})
}

func (m *Manager) serviceEnded(st *state, es exitStatus) {
	s := st.u.Service
	m.ulog(st, "Main process %s", es)
	result := "success"
	if !es.success(s) {
		result = es.result()
	}
	restart := !m.shuttingDown && shouldRestart(s, es)
	m.teardown(st, true, func() {
		if st.stopResult != "" && result == "success" {
			result = st.stopResult
		}
		if restart {
			m.scheduleRestart(st, result)
			return
		}
		m.finalize(st, result)
	})
}

func (m *Manager) scheduleRestart(st *state, result string) {
	s := st.u.Service
	st.result = result
	m.closeNotifier(st)
	m.setState(st, stateActivating, "auto-restart")
	m.ulog(st, "Scheduled restart in %s (result: %s)", s.RestartDelay, result)
	st.restart = time.AfterFunc(s.RestartDelay, func() {
		m.post(func() {
			if st.restart == nil || st.sub != "auto-restart" {
				return
			}
			st.restart = nil
			m.enqueueStart(st.name(), func(err error) {
				if err != nil {
					m.ulog(st, "Restart failed: %v", err)
				}
			})
		})
	})
}

func (m *Manager) finalize(st *state, result string) {
	m.closeNotifier(st)
	st.result = result
	st.statusText = ""
	if result == "success" || result == "condition" {
		m.setState(st, stateInactive, "dead")
	} else {
		m.setState(st, stateFailed, "failed")
		m.ulog(st, "Failed with result %s", result)
	}
	m.unitDeactivated(st)
	if m.shuttingDown {
		return
	}
	hooks := st.u.OnSuccess
	if st.active == stateFailed {
		hooks = st.u.OnFailure
	}
	for _, h := range hooks {
		m.enqueueStart(h, func(err error) {
			if err != nil {
				m.ulog(st, "Starting %s after %s failed: %v", h, result, err)
			}
		})
	}
}

func (m *Manager) stopService(st *state) {
	m.disarm(st)
	st.onControl, st.onMain = nil, nil
	wasActive := st.active == stateActive || st.active == stateReloading
	if st.main == nil && st.control == nil && !wasActive {
		if st.active == stateActivating || st.active == stateDeactivating {
			m.setState(st, stateInactive, "dead")
		}
		m.finishJob(st.name(), nil)
		return
	}
	st.stopResult = ""
	m.teardown(st, wasActive, func() {
		result := st.stopResult
		if result == "" {
			result = "success"
		}
		m.ulog(st, "Stopped")
		m.finalize(st, result)
		m.finishJob(st.name(), nil)
	})
}

func (m *Manager) teardown(st *state, runStop bool, then func()) {
	s := st.u.Service
	m.setState(st, stateDeactivating, "stop")
	st.onMain = nil
	afterStop := func() {
		m.killPhase(st, func() {
			m.setState(st, stateDeactivating, "stop-post")
			m.arm(st, s.TimeoutStop, func() {
				st.stopResult = "timeout"
				m.killControl(st)
			})
			m.runSeq(st, s.ExecStopPost, "ExecStopPost", func(es exitStatus, failed bool) {
				m.disarm(st)
				if failed && st.stopResult == "" {
					st.stopResult = es.result()
				}
				then()
			})
		})
	}
	if !runStop || len(s.ExecStop) == 0 {
		afterStop()
		return
	}
	m.arm(st, s.TimeoutStop, func() {
		m.ulog(st, "ExecStop timed out after %s", s.TimeoutStop)
		st.stopResult = "timeout"
		m.killControl(st)
	})
	m.runSeq(st, s.ExecStop, "ExecStop", func(es exitStatus, failed bool) {
		m.disarm(st)
		if failed && st.stopResult == "" {
			st.stopResult = es.result()
		}
		afterStop()
	})
}

func (m *Manager) killControl(st *state) {
	if st.control != nil {
		m.signal(st, st.control.pid, syscall.SIGKILL, true)
	}
}

func (m *Manager) killPhase(st *state, then func()) {
	s := st.u.Service
	st.onGone = then
	if m.gone(st) {
		m.fireGone(st)
		return
	}
	m.setState(st, stateDeactivating, "stop-sigterm")
	m.sendKill(st, s.KillSignal, false)
	m.arm(st, s.TimeoutStop, func() {
		if !s.SendSIGKILL {
			m.ulog(st, "Processes still running after %s, SendSIGKILL=no leaves them", s.TimeoutStop)
			m.fireGone(st)
			return
		}
		m.ulog(st, "Stop timed out after %s, killing", s.TimeoutStop)
		if st.stopResult == "" {
			st.stopResult = "timeout"
		}
		m.setState(st, stateDeactivating, "stop-sigkill")
		m.sendKill(st, syscall.SIGKILL, true)
		m.arm(st, finalKillWait, func() {
			m.ulog(st, "Processes survived SIGKILL for %s, giving up on them", finalKillWait)
			m.fireGone(st)
		})
	})
	m.checkGone(st)
}

func (m *Manager) sendKill(st *state, sig syscall.Signal, final bool) {
	mode := st.u.Service.KillMode
	if mode == unit.KillNone {
		return
	}
	group := mode == unit.KillControlGroup || (mode == unit.KillMixed && final)
	if group {
		for _, pgid := range st.groups {
			m.signal(st, pgid, sig, true)
		}
		return
	}
	for _, p := range []*process{st.main, st.control} {
		if p != nil {
			m.signal(st, p.pid, sig, false)
		}
	}
}

func (m *Manager) signal(st *state, pid int, sig syscall.Signal, group bool) {
	var err error
	if group {
		err = signalGroup(pid, sig)
	} else {
		err = signalProcess(pid, sig)
	}
	if err != nil {
		m.ulog(st, "%v", err)
	}
}

func (m *Manager) gone(st *state) bool {
	if st.main != nil || st.control != nil {
		return false
	}
	mode := st.u.Service.KillMode
	if mode == unit.KillProcess || mode == unit.KillNone {
		return true
	}
	alive := st.groups[:0]
	for _, g := range st.groups {
		if groupAlive(g) {
			alive = append(alive, g)
		}
	}
	st.groups = alive
	return len(alive) == 0
}

func (m *Manager) checkGone(st *state) {
	if st.onGone == nil {
		return
	}
	if m.gone(st) {
		m.fireGone(st)
		return
	}
	if st.main != nil || st.control != nil || st.goneProbe {
		return
	}
	st.goneProbe = true
	time.AfterFunc(goneProbeInterval, func() {
		m.post(func() {
			st.goneProbe = false
			m.checkGone(st)
		})
	})
}

func (m *Manager) fireGone(st *state) {
	m.disarm(st)
	then := st.onGone
	st.onGone = nil
	st.groups = nil
	if then != nil {
		then()
	}
}

func (m *Manager) runSeq(st *state, cmds []unit.Command, role string, done func(exitStatus, bool)) {
	if len(cmds) == 0 {
		done(exitStatus{}, false)
		return
	}
	c := cmds[0]
	p, err := m.spawn(st, role, c)
	if err != nil {
		m.ulog(st, "%s: %v", role, err)
		if c.IgnoreFailure {
			m.runSeq(st, cmds[1:], role, done)
			return
		}
		done(exitStatus{spawnErr: err}, true)
		return
	}
	st.control = p
	st.onControl = func(es exitStatus) {
		if !controlSucceeded(es) && !c.IgnoreFailure {
			done(es, true)
			return
		}
		m.runSeq(st, cmds[1:], role, done)
	}
}

func (m *Manager) exited(st *state, p *process, es exitStatus) {
	p.done = true
	switch p {
	case st.main:
		st.main = nil
		if !controlSucceeded(es) {
			m.ulog(st, "Process %d (%s) %s", p.pid, p.role, es)
		}
		m.mainExited(st, es)
	case st.control:
		st.control = nil
		if !controlSucceeded(es) {
			m.ulog(st, "Process %d (%s) %s", p.pid, p.role, es)
		}
		if f := st.onControl; f != nil {
			st.onControl = nil
			f(es)
			return
		}
		m.checkGone(st)
	}
}

func (m *Manager) mainExited(st *state, es exitStatus) {
	if f := st.onMain; f != nil {
		st.onMain = nil
		f(es)
		return
	}
	switch st.active {
	case stateActivating:
		if st.sub == "start" && st.u.Service.Type.Notifies() {
			m.failActivation(st, es, fmt.Errorf("%s: main process %s before signalling readiness", st.name(), es))
			return
		}
		saved := es
		st.mainExit = &saved
	case stateActive, stateReloading:
		if st.reloadWaiter != nil {
			st.reloadWaiter(fmt.Errorf("%s: main process %s during reload", st.name(), es))
			st.reloadWaiter = nil
		}
		m.serviceEnded(st, es)
	default:
		m.checkGone(st)
	}
}

func (m *Manager) spawn(st *state, role string, c unit.Command) (*process, error) {
	s := st.u.Service
	env := st.env
	if st.main != nil && role != "main" {
		env = env.Clone()
		env["MAINPID"] = strconv.Itoa(st.main.pid)
	}
	path, err := lookPath(c.Path, env["PATH"])
	if err != nil {
		return nil, err
	}
	argv := c.Argv(env.Lookup)
	cmdPath, cmdArgs := path, argv
	attrs := attributes{nice: s.Nice, ioClass: s.IOSchedulingClass, ioPrio: s.IOSchedulingPriority, umask: s.UMask}
	if attrs.any() {
		cmdPath = m.helper
		cmdArgs = append([]string{m.helper, "service", "exec"}, attrs.helperArgs()...)
		cmdArgs = append(append(cmdArgs, "--", path), argv...)
	}
	dir := s.WorkingDirectory
	if dir == "" {
		dir = m.layout.Host.Home
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		if !s.WorkingDirectoryOptional {
			return nil, fmt.Errorf("working directory %s is not a directory", dir)
		}
		dir = m.layout.Host.Home
	}
	stdout, stderr, cleanup, err := m.outputs(st)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cmd := &exec.Cmd{
		Path:        cmdPath,
		Args:        cmdArgs,
		Env:         env.Environ(),
		Dir:         dir,
		Stdout:      stdout,
		Stderr:      stderr,
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", proc.Quote(argv), err)
	}
	p := &process{pid: cmd.Process.Pid, role: role, cmd: cmd}
	st.groups = append(st.groups, p.pid)
	m.ulog(st, "Started %s (pid %d): %s", role, p.pid, proc.Quote(argv))
	go func() {
		err := cmd.Wait()
		es := exitStatus{spawnErr: err}
		if cmd.ProcessState != nil {
			es = statusOf(cmd.ProcessState)
		}
		m.post(func() { m.exited(st, p, es) })
	}()
	return p, nil
}

func (m *Manager) outputs(st *state) (*os.File, *os.File, func(), error) {
	s := st.u.Service
	var opened []*os.File
	cleanup := func() {
		for _, f := range opened {
			f.Close()
		}
	}
	var journalW *os.File
	open := func(o unit.Output, fallback *os.File) (*os.File, error) {
		switch o.Kind {
		case unit.OutputInherit:
			return fallback, nil
		case unit.OutputNull:
			f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			if err != nil {
				return nil, fmt.Errorf("opening %s: %w", os.DevNull, err)
			}
			opened = append(opened, f)
			return f, nil
		case unit.OutputJournal:
			if journalW != nil {
				return journalW, nil
			}
			r, w, err := os.Pipe()
			if err != nil {
				return nil, fmt.Errorf("creating the output pipe: %w", err)
			}
			journalW = w
			opened = append(opened, w)
			go m.journal.Pump(st.name().String(), r, m.reportErr)
			return w, nil
		}
		flags := os.O_WRONLY | os.O_CREATE
		switch o.Kind {
		case unit.OutputAppend:
			flags |= os.O_APPEND
		case unit.OutputTruncate:
			flags |= os.O_TRUNC
		}
		f, err := os.OpenFile(o.Path, flags, 0o600)
		if err != nil {
			return nil, fmt.Errorf("opening output %s: %w", o.Path, err)
		}
		opened = append(opened, f)
		return f, nil
	}
	nullOut := unit.Output{Kind: unit.OutputNull}
	stdoutSpec := s.Stdout
	if stdoutSpec.Kind == unit.OutputInherit {
		stdoutSpec = nullOut
	}
	stdout, err := open(stdoutSpec, nil)
	if err != nil {
		cleanup()
		return nil, nil, nil, err
	}
	stderr, err := open(s.Stderr, stdout)
	if err != nil {
		cleanup()
		return nil, nil, nil, err
	}
	return stdout, stderr, cleanup, nil
}

func (m *Manager) reload(st *state, waiter func(error)) {
	s := st.u.Service
	if s == nil || (st.active != stateActive) {
		waiter(fmt.Errorf("%s: cannot reload, the unit is %s", st.name(), st.active))
		return
	}
	switch {
	case len(s.ExecReload) > 0:
		m.setState(st, stateReloading, "reload")
		m.arm(st, s.TimeoutStart, func() {
			m.killControl(st)
		})
		m.runSeq(st, s.ExecReload, "ExecReload", func(es exitStatus, failed bool) {
			m.disarm(st)
			if st.active == stateReloading {
				m.setState(st, stateActive, st.sub0())
			}
			if failed {
				waiter(fmt.Errorf("%s: ExecReload %s", st.name(), es))
				return
			}
			m.ulog(st, "Reloaded")
			waiter(nil)
		})
	case s.Type == unit.NotifyReload && st.main != nil:
		m.setState(st, stateReloading, "reload-signal")
		st.reloadWaiter = waiter
		m.signal(st, st.main.pid, s.ReloadSignal, false)
		m.arm(st, s.TimeoutStart, func() {
			if st.reloadWaiter != nil {
				st.reloadWaiter(fmt.Errorf("%s: reload timed out after %s", st.name(), s.TimeoutStart))
				st.reloadWaiter = nil
			}
			m.setState(st, stateActive, "running")
		})
	default:
		waiter(fmt.Errorf("%s does not support reload: it has no ExecReload= and is not Type=notify-reload", st.name()))
	}
}

func (st *state) sub0() string {
	if st.main != nil {
		return "running"
	}
	return "exited"
}
