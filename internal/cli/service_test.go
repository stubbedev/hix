package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/stubbedev/phix/internal/supervisor"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "service" {
		os.Exit(Main(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func TestServiceRefusesOnNixOS(t *testing.T) {
	h := newHarness(t)
	_, errOut, code := h.run("", "service")
	if code != 1 || !strings.Contains(errOut, "systemctl --user") {
		t.Fatalf("phix service on NixOS: exit %d, stderr %q", code, errOut)
	}
}

func TestWriteChangesMatchesSwitchToConfiguration(t *testing.T) {
	var out bytes.Buffer
	writeChanges(&out, []supervisor.Change{
		{Action: supervisor.ActionStart, Unit: "new.service"},
		{Action: supervisor.ActionStop, Unit: "gone.service"},
		{Action: supervisor.ActionRestart, Unit: "a.service"},
		{Action: supervisor.ActionRestart, Unit: "b.timer"},
	})
	want := "stopping the following user units: gone.service\n" +
		"restarting the following user units: a.service, b.timer\n" +
		"starting the following user units: new.service\n"
	if out.String() != want {
		t.Errorf("writeChanges:\n%s\nwant:\n%s", out.String(), want)
	}
}

func androidHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	cfg, err := yaml.Marshal(map[string]any{
		"flake":    h.flake,
		"platform": "android",
		"attr":     "phone",
		"ssh_key":  filepath.Join(h.home, ".ssh", "id_ed25519"),
	})
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(h.dir, "android.yaml")
	if err := os.WriteFile(cfgPath, cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHIX_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h.home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(h.home, ".local", "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(h.home, ".local", "share"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(h.dir, "run"))
	units := filepath.Join(h.home, ".config", "systemd", "user")
	if err := os.MkdirAll(filepath.Join(units, "default.target.wants"), 0o755); err != nil {
		t.Fatal(err)
	}
	return h, units
}

func writeUnit(t *testing.T, units, name, content string) {
	t.Helper()
	src := filepath.Join(units, "..", "store-"+name)
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{filepath.Join(units, name), filepath.Join(units, "default.target.wants", name)} {
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.Symlink(src, link); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServiceLifecycle(t *testing.T) {
	h, units := androidHarness(t)
	writeUnit(t, units, "app.service", "[Unit]\nDescription=Demo\n[Service]\nExecStart=/bin/sh -c 'echo hello; exec sleep 3600'\nProtectHome=yes\n")
	t.Cleanup(func() { h.run("", "service", "down") })

	out := h.mustRun("", "service", "sync")
	if out != "starting the following user units: app.service\n" {
		t.Errorf("first sync = %q, want it to boot the supervisor and report app.service", out)
	}
	if _, errOut, code := h.run("", "service", "up", "-v"); code != 0 || !strings.Contains(errOut, "already running") {
		t.Errorf("service up: exit %d, stderr %q", code, errOut)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(h.mustRun("", "service", "logs", "app"), "hello") {
		if time.Now().After(deadline) {
			t.Fatalf("app never logged its greeting: %q", h.mustRun("", "service", "logs", "app"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	table := h.mustRun("", "service")
	if !strings.Contains(table, "app.service") || !strings.Contains(table, "active (running)") {
		t.Errorf("status table:\n%s", table)
	}
	detail := h.mustRun("", "service", "status", "app")
	for _, want := range []string{"app.service - Demo", "Ignored: Service.ProtectHome", "Main PID:", "hello"} {
		if !strings.Contains(detail, want) {
			t.Errorf("status app lacks %q:\n%s", want, detail)
		}
	}

	writeUnit(t, units, "app.service", "[Service]\nExecStart=/bin/sh -c 'echo again; exec sleep 3600'\n")
	if out := h.mustRun("", "service", "sync"); out != "restarting the following user units: app.service\n" {
		t.Errorf("sync after a change = %q", out)
	}
	if out := h.mustRun("", "service", "sync"); out != "" {
		t.Errorf("sync without changes = %q, want nothing", out)
	}
	h.mustRun("", "service", "stop", "app")
	if !strings.Contains(h.mustRun("", "service", "status", "app"), "inactive (dead)") {
		t.Error("app is not inactive after stop")
	}
	if _, errOut, code := h.run("", "service", "start", "missing"); code != 1 || !strings.Contains(errOut, "missing.service") {
		t.Errorf("starting a missing unit: exit %d, stderr %q", code, errOut)
	}
	h.mustRun("", "service", "down")
	if !strings.Contains(h.mustRun("", "service", "logs"), "stopped") {
		t.Error("the supervisor log is unreadable or lacks its shutdown once the supervisor is down")
	}
	if _, errOut, code := h.run("", "service"); code != 1 || !strings.Contains(errOut, "not running") {
		t.Errorf("status after down: exit %d, stderr %q", code, errOut)
	}
}
