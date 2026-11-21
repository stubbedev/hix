package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stubbedev/phix/internal/supervisor"
	"github.com/stubbedev/phix/internal/usage"
)

var systemctlIgnoredFlags = []string{"--user", "--no-pager", "--no-legend", "--full", "-l", "--all", "-a", "--no-ask-password", "--plain", "--now"}

type systemctlArgs struct {
	verb  string
	units []string
	quiet bool
}

func parseSystemctl(args []string) (systemctlArgs, error) {
	var out systemctlArgs
	user := false
	for _, a := range args {
		switch {
		case a == "--user":
			user = true
		case a == "--system":
			return out, usage.Errorf("systemctl --system: there is no system manager on Android, only --user units")
		case a == "-q" || a == "--quiet":
			out.quiet = true
		case a == "--no-block":
			return out, usage.Errorf("systemctl --no-block is not supported: phix waits for every job")
		case slices.Contains(systemctlIgnoredFlags, a):
		case strings.HasPrefix(a, "-"):
			return out, usage.Errorf("systemctl: option %s is not supported", a)
		case out.verb == "":
			out.verb = a
		default:
			out.units = append(out.units, a)
		}
	}
	if !user {
		return out, usage.Errorf("systemctl: pass --user; Android has no system manager, phix runs the user units")
	}
	if out.verb == "" {
		out.verb = "list-units"
	}
	return out, nil
}

func (a *App) systemctlCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "systemctl --user <command> [unit...]",
		Short:              "systemctl --user for scripts, answered by the phix supervisor",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := parseSystemctl(args)
			if err != nil {
				return err
			}
			return a.systemctl(cmd.Context(), p)
		},
	}
}

func (a *App) systemctl(ctx context.Context, p systemctlArgs) error {
	layout, err := a.serviceLayout()
	if err != nil {
		return err
	}
	client := supervisor.NewClient(layout)
	needUnits := func() error {
		if len(p.units) == 0 {
			return usage.Errorf("systemctl %s needs at least one unit", p.verb)
		}
		return nil
	}
	control := func(op supervisor.Op, units []string) error {
		resp, err := client.Call(ctx, supervisor.Request{Op: op, Units: units})
		if err != nil {
			return err
		}
		return failuresError(op, resp.Failures)
	}
	switch p.verb {
	case "start", "stop", "restart", "reload":
		if err := needUnits(); err != nil {
			return err
		}
		return control(supervisor.Op(p.verb), p.units)
	case "try-restart", "condrestart", "reload-or-restart", "try-reload-or-restart":
		if err := needUnits(); err != nil {
			return err
		}
		statuses, err := a.unitStatuses(ctx, client, p.units)
		if err != nil {
			return err
		}
		var active []string
		for _, s := range statuses {
			if s.Active == "active" || p.verb == "reload-or-restart" {
				active = append(active, s.Name)
			}
		}
		if len(active) == 0 {
			return nil
		}
		return control(supervisor.OpRestart, active)
	case "status":
		return a.serviceStatus(ctx, p.units, false)
	case "list-units", "list-timers", "list-sockets":
		return a.serviceStatus(ctx, nil, false)
	case "is-active", "is-failed", "is-enabled":
		return a.systemctlIs(ctx, client, p)
	case "is-system-running":
		return a.systemRunning(ctx, client, p.quiet)
	case "daemon-reload":
		_, err := client.Call(ctx, supervisor.Request{Op: supervisor.OpDaemonReload})
		return err
	case "reset-failed":
		_, err := client.Call(ctx, supervisor.Request{Op: supervisor.OpResetFailed, Units: p.units})
		return err
	case "cat":
		if err := needUnits(); err != nil {
			return err
		}
		return a.catUnits(ctx, client, p.units)
	case "enable", "disable", "mask", "unmask", "link", "reenable", "preset":
		return fmt.Errorf("systemctl %s: units are enabled through home-manager (Install.WantedBy); use start or stop to change what runs now", p.verb)
	}
	return usage.Errorf("systemctl %s is not supported", p.verb)
}

func (a *App) unitStatuses(ctx context.Context, client supervisor.Client, units []string) ([]supervisor.UnitStatus, error) {
	resp, err := client.Call(ctx, supervisor.Request{Op: supervisor.OpStatus, Units: units})
	if err != nil {
		return nil, err
	}
	return resp.Status, nil
}

func (a *App) systemctlIs(ctx context.Context, client supervisor.Client, p systemctlArgs) error {
	if len(p.units) == 0 {
		return usage.Errorf("systemctl %s needs at least one unit", p.verb)
	}
	all := true
	for _, u := range p.units {
		statuses, err := a.unitStatuses(ctx, client, []string{u})
		answer, ok := "inactive", false
		switch {
		case err != nil && p.verb == "is-enabled":
			answer = "not-found"
		case err != nil:
		case p.verb == "is-active":
			answer, ok = statuses[0].Active, statuses[0].Active == "active" || statuses[0].Active == "reloading"
		case p.verb == "is-failed":
			answer, ok = statuses[0].Active, statuses[0].Active == "failed"
		default:
			answer, ok = "static", true
			if statuses[0].Wanted {
				answer = "enabled"
			}
		}
		all = all && ok
		if !p.quiet {
			fmt.Fprintln(a.console.Stdout, answer)
		}
	}
	if !all {
		return &silentExit{code: 1}
	}
	return nil
}

func (a *App) systemRunning(ctx context.Context, client supervisor.Client, quiet bool) error {
	statuses, err := a.unitStatuses(ctx, client, nil)
	state := "running"
	switch {
	case errors.Is(err, supervisor.ErrNotRunning):
		state = "offline"
	case err != nil:
		return err
	default:
		for _, s := range statuses {
			if s.Job != "" && state == "running" {
				state = "starting"
			}
			if s.Active == "failed" || s.LoadError != "" {
				state = "degraded"
			}
		}
	}
	if !quiet {
		fmt.Fprintln(a.console.Stdout, state)
	}
	if state != "running" {
		return &silentExit{code: 1}
	}
	return nil
}

func (a *App) catUnits(ctx context.Context, client supervisor.Client, units []string) error {
	statuses, err := a.unitStatuses(ctx, client, units)
	if err != nil {
		return err
	}
	for i, s := range statuses {
		if i > 0 {
			fmt.Fprintln(a.console.Stdout)
		}
		if s.Fragment == "" {
			fmt.Fprintf(a.console.Stdout, "# %s is built in\n", s.Name)
			continue
		}
		data, err := os.ReadFile(s.Fragment)
		if err != nil {
			return fmt.Errorf("reading %s: %w", s.Fragment, err)
		}
		fmt.Fprintf(a.console.Stdout, "# %s\n%s", s.Fragment, data)
	}
	return nil
}

type journalctlArgs struct {
	unit   string
	lines  int
	follow bool
	bare   bool
}

func parseJournalctl(args []string) (journalctlArgs, error) {
	out := journalctlArgs{lines: -1}
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, error) {
			if name, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(name, "--") {
				return v, nil
			}
			if i+1 >= len(args) {
				return "", usage.Errorf("journalctl %s needs a value", a)
			}
			i++
			return args[i], nil
		}
		flag, _, _ := strings.Cut(a, "=")
		switch flag {
		case "--user", "--no-pager", "-e", "--pager-end", "-b", "--boot", "-q", "--quiet":
		case "-f", "--follow":
			out.follow = true
		case "-u", "--unit", "--user-unit":
			v, err := value()
			if err != nil {
				return out, err
			}
			if out.unit != "" {
				return out, usage.Errorf("journalctl: only one unit at a time is supported")
			}
			out.unit = v
		case "-n", "--lines":
			v, err := value()
			if err != nil {
				return out, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return out, usage.Errorf("journalctl --lines %q is not a non-negative number", v)
			}
			out.lines = n
		case "-o", "--output":
			v, err := value()
			if err != nil {
				return out, err
			}
			switch v {
			case "cat":
				out.bare = true
			case "short", "short-iso", "short-precise":
			default:
				return out, usage.Errorf("journalctl --output %s is not supported (supported: short, cat)", v)
			}
		default:
			return out, usage.Errorf("journalctl: option %s is not supported", a)
		}
	}
	return out, nil
}

func (a *App) journalctlCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "journalctl --user [-u unit] [-f] [-n N] [-o cat]",
		Short:              "journalctl --user for scripts, reading the phix service logs",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			j, err := parseJournalctl(args)
			if err != nil {
				return err
			}
			layout, err := a.serviceLayout()
			if err != nil {
				return err
			}
			var units []string
			if j.unit != "" {
				units = []string{j.unit}
			}
			emit := func(line string) {
				if j.bare {
					if _, rest, ok := strings.Cut(line, " "); ok {
						line = rest
					}
				}
				fmt.Fprintln(a.console.Stdout, line)
			}
			if !j.follow {
				name, err := supervisor.LogName(units)
				if err != nil {
					return err
				}
				lines, err := supervisor.ReadLog(layout, name, j.lines)
				if err != nil {
					return err
				}
				for _, l := range lines {
					emit(l)
				}
				return nil
			}
			n := j.lines
			if n < 0 {
				n = 10
			}
			err = supervisor.NewClient(layout).Stream(cmd.Context(), supervisor.Request{Op: supervisor.OpFollow, Units: units, Lines: n}, func(r supervisor.Response) error {
				if r.Line != "" {
					emit(r.Line)
				}
				return nil
			})
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		},
	}
}
