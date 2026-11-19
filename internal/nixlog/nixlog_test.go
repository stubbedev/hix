package nixlog

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/phix/internal/ui"
)

var epoch = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func fixedClock(t *time.Time) func() time.Time {
	return func() time.Time { return *t }
}

func render(t *testing.T, fixture string, cfg Config) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	now := epoch
	cfg.Now = fixedClock(&now)
	var out bytes.Buffer
	if err := NewRenderer(&out, cfg).Consume(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestParse(t *testing.T) {
	ev, ok, err := Parse([]byte(`@nix {"action":"start","fields":["/nix/store/lbn72v74by31g9w4dxxmw5ac1yklq6z6-hello-1.0.drv","",1,1],"id":7,"level":3,"parent":2,"text":"building","type":105}`))
	if err != nil || !ok {
		t.Fatalf("Parse: ok=%v err=%v", ok, err)
	}
	if ev.Action != ActionStart || ev.ID != 7 || ev.Parent != 2 || ActivityType(ev.Type) != ActivityBuild || ev.Level != LevelInfo {
		t.Fatalf("unexpected event %+v", ev)
	}
	if s, ok := ev.StringField(0); !ok || !strings.HasSuffix(s, "-hello-1.0.drv") {
		t.Fatalf("StringField(0) = %q, %v", s, ok)
	}
	if n, ok := ev.IntField(2); !ok || n != 1 {
		t.Fatalf("IntField(2) = %d, %v", n, ok)
	}
	if _, ok := ev.IntField(1); ok {
		t.Fatal("IntField on a string field succeeded")
	}
	if _, ok := ev.StringField(9); ok {
		t.Fatal("StringField out of range succeeded")
	}

	if _, ok, err := Parse([]byte("warning: plain line")); ok || err != nil {
		t.Fatalf("plain line: ok=%v err=%v", ok, err)
	}
	if _, ok, err := Parse([]byte(`@nix {"action":"bogus"}`)); !ok || err == nil {
		t.Fatalf("unknown action: ok=%v err=%v", ok, err)
	}
	if _, ok, err := Parse([]byte(`@nix {not json`)); !ok || err == nil {
		t.Fatalf("malformed: ok=%v err=%v", ok, err)
	}
}

func TestPlainBuild(t *testing.T) {
	out := render(t, "build.jsonl", Config{})
	want := "this derivation will be built:\n" +
		"  /nix/store/lbn72v74by31g9w4dxxmw5ac1yklq6z6-hm-fixture-1794938904.drv\n" +
		"building '/nix/store/lbn72v74by31g9w4dxxmw5ac1yklq6z6-hm-fixture-1794938904.drv'\n"
	if out != want {
		t.Fatalf("plain output:\n%q\nwant:\n%q", out, want)
	}
}

func TestBuildLogs(t *testing.T) {
	out := render(t, "build.jsonl", Config{BuildLogs: true})
	for _, line := range []string{"hm-fixture-1794938904> hello from build\n", "hm-fixture-1794938904> second line\n"} {
		if !strings.Contains(out, line) {
			t.Fatalf("missing %q in:\n%s", line, out)
		}
	}
}

func TestFailureMessage(t *testing.T) {
	out := render(t, "fail.jsonl", Config{})
	if !strings.Contains(out, "builder failed with exit code 3") || !strings.Contains(out, "> about to fail") {
		t.Fatalf("failure not reported:\n%s", out)
	}
}

func TestPlainFetch(t *testing.T) {
	out := render(t, "fetch.jsonl", Config{})
	if !strings.Contains(out, "copying path '/nix/store/i0p3zh00jyq3l7hw8qm56mqak5pp3bs8-sl-5.05' from 'https://cache.nixos.org'\n") {
		t.Fatalf("copy not reported:\n%s", out)
	}
	if strings.Contains(out, "downloading") || strings.Contains(out, "querying info") {
		t.Fatalf("talkative activities leaked into plain output:\n%s", out)
	}
}

func TestLiveFetch(t *testing.T) {
	const width = 40
	out := render(t, "fetch.jsonl", Config{Width: width, Height: 20})
	if strings.Contains(out, "copying path") || strings.Contains(out, "will be fetched") {
		t.Fatalf("live output prints what the frame shows:\n%q", out)
	}
	if !strings.HasPrefix(out, syncStart+hideCursor) || !strings.HasSuffix(out, showCursor+syncEnd) {
		t.Fatalf("live output does not hide and restore the cursor:\n%q", out)
	}
	ic := ui.Unicode
	summary := ic.With(ic.Done, "Finished at 12:00:00 after 0s: fetched 1 (10.52 KiB from cache.nixos.org)") + clearLine + "\n"
	frames, _, ok := strings.Cut(out, summary)
	if !ok {
		t.Fatalf("no final summary:\n%q", out)
	}
	for segment := range strings.SplitSeq(frames, "\n") {
		for line := range strings.FieldsFuncSeq(segment, func(r rune) bool { return r == '\x1b' }) {
			if ui.Width(line) >= width {
				t.Fatalf("frame line not narrower than the terminal: %q", line)
			}
		}
	}
}

type watchedBuffer struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	writes chan struct{}
}

func (w *watchedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	select {
	case w.writes <- struct{}{}:
	default:
	}
	return n, err
}

func (w *watchedBuffer) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		w.mu.Lock()
		found := strings.Contains(w.buf.String(), want)
		w.mu.Unlock()
		if found {
			return
		}
		select {
		case <-w.writes:
		case <-deadline:
			t.Fatalf("output never contained %q", want)
		}
	}
}

func TestLiveFrameDrawsWhileStreaming(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "fetch.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(data), "\n")
	split := slices.IndexFunc(lines, func(l string) bool {
		return strings.Contains(l, `"type":100}`) && strings.Contains(l, `"action":"start"`)
	}) + 1
	if split == 0 {
		t.Fatal("fetch.jsonl has no copy-path activity")
	}
	out := &watchedBuffer{writes: make(chan struct{}, 1)}
	in, feed := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- NewRenderer(out, Config{Width: 80, Height: 20}).Consume(in) }()
	if _, err := io.WriteString(feed, strings.Join(lines[:split], "")); err != nil {
		t.Fatal(err)
	}
	ic := ui.Unicode
	out.waitFor(t, "├─ "+ic.Download+ic.Gap+"sl-5.05")
	if _, err := io.WriteString(feed, strings.Join(lines[split:], "")); err != nil {
		t.Fatal(err)
	}
	feed.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	summary := ic.With(ic.Done, "Finished at")
	out.waitFor(t, summary)
	final := out.buf.String()
	final = final[strings.LastIndex(final, syncStart):]
	if !strings.HasPrefix(final, syncStart+"\r") || strings.Index(final, clearToEnd) < strings.Index(final, summary) {
		t.Fatalf("frame wiped before the summary replaced it: %q", final)
	}
}

func TestLiveFailure(t *testing.T) {
	out := render(t, "fail.jsonl", Config{Width: 80, Height: 20})
	ic := ui.Unicode
	want := ic.With(ic.Failed, "1 failed build(s) after 0s") + clearLine + "\n" +
		"╰─ " + ic.With(ic.Failed, "hm-fail-1794938919") + ": builder failed with exit code 3" + clearLine + "\n" +
		"   │ about to fail" + clearLine + "\n" +
		"   full log: nix log /nix/store/difkgyrh68ns4bwylg87b52621cjvg5j-hm-fail-1794938919.drv" + clearLine + "\n"
	if !strings.Contains(out, want) {
		t.Fatalf("failure not grouped:\n%s\nwant:\n%s", out, want)
	}
	if strings.Contains(out, "Cannot build") {
		t.Fatalf("raw nix failure printed besides the grouped report:\n%s", out)
	}
}

func TestCancelledBuildsAreCountedNotReported(t *testing.T) {
	r := &replay{t: t, now: epoch, state: NewState(Options{Live: true}, epoch)}
	r.apply(
		`@nix {"action":"msg","level":3,"msg":"these 2 derivations will be built:"}`,
		`@nix {"action":"msg","level":3,"msg":"  `+drvPath("leaf")+`"}`,
		`@nix {"action":"msg","level":3,"msg":"  `+drvPath("top")+`"}`,
		`@nix {"action":"msg","level":0,"msg":"error: Cannot build '`+drvPath("leaf")+`'.\n       Reason: builder failed with exit code 1.\n       Last 2 log lines:\n       > one\n       > two"}`,
		`@nix {"action":"msg","level":0,"msg":"error: Cannot build '`+drvPath("top")+`'.\n       Reason: 1 dependency failed."}`,
	)
	ic := ui.Unicode
	want := []string{
		ic.With(ic.Failed, "1 failed build(s), 1 cancelled after 0s"),
		"╰─ " + ic.With(ic.Failed, "leaf") + ": builder failed with exit code 1",
		"   │ one",
		"   │ two",
		"   full log: nix log " + drvPath("leaf"),
	}
	if got := r.state.Summary(ui.Style{}, epoch); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("summary:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func drvPath(name string) string {
	return "/nix/store/" + strings.Repeat("a", 32) + "-" + name + ".drv"
}

type replay struct {
	t     *testing.T
	state *State
	now   time.Time
}

func (r *replay) apply(lines ...string) {
	r.t.Helper()
	for _, line := range lines {
		ev, _, err := Parse([]byte(line))
		if err != nil {
			r.t.Fatal(err)
		}
		if _, err := r.state.Apply(ev, r.now); err != nil {
			r.t.Fatal(err)
		}
	}
}

func TestTree(t *testing.T) {
	inputs := map[string][]string{
		drvPath("system"): {drvPath("etc"), drvPath("kernel")},
		drvPath("etc"):    {drvPath("unit"), drvPath("unrelated-input")},
	}
	r := &replay{t: t, now: epoch, state: NewState(Options{Live: true, Inputs: func(p string) ([]string, error) { return inputs[p], nil }}, epoch)}
	r.apply(
		`@nix {"action":"msg","level":3,"msg":"these 4 derivations will be built:"}`,
		`@nix {"action":"msg","level":3,"msg":"  `+drvPath("unit")+`"}`,
		`@nix {"action":"msg","level":3,"msg":"  `+drvPath("kernel")+`"}`,
		`@nix {"action":"msg","level":3,"msg":"  `+drvPath("etc")+`"}`,
		`@nix {"action":"msg","level":3,"msg":"  `+drvPath("system")+`"}`,
		`@nix {"action":"start","id":1,"level":0,"type":104,"text":""}`,
		`@nix {"action":"result","id":1,"type":105,"fields":[0,4,2,0]}`,
		`@nix {"action":"start","id":2,"level":3,"type":105,"text":"","fields":["`+drvPath("unit")+`","",1,1]}`,
		`@nix {"action":"result","id":2,"type":104,"fields":["buildPhase"]}`,
		`@nix {"action":"start","id":3,"level":3,"type":105,"text":"","fields":["`+drvPath("kernel")+`","",1,1]}`,
	)
	r.now = epoch.Add(65 * time.Second)
	ic := ui.Unicode
	want := []string{
		"├─ " + ic.Planned + ic.Gap + "system",
		"│  ├─ " + ic.Running + ic.Gap + "kernel  " + ic.With(ic.Clock, "1m5s"),
		"│  ╰─ " + ic.Planned + ic.Gap + "etc",
		"│     ╰─ " + ic.Running + ic.Gap + "unit  buildPhase  " + ic.With(ic.Clock, "1m5s"),
		"┝━━━ Builds",
		"┕━ " + ic.Summary + ic.Gap + ic.With(ic.Running, "2") + " " + ic.With(ic.Done, "0") + " " + ic.With(ic.Planned, "2") + " │ " + ic.With(ic.Clock, "1m5s"),
	}
	got := r.state.Frame(View{Width: 200, Height: 40}, r.now)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("frame:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	flat := r.state.Frame(View{Width: 200, Height: 5}, r.now)
	if len(flat) != 4 || !strings.HasPrefix(flat[0], "├─ "+ic.Running) || !strings.Contains(flat[0], "kernel") || !strings.Contains(flat[1], "unit") {
		t.Fatalf("frame does not flatten into 5 rows:\n%s", strings.Join(flat, "\n"))
	}
	cut := r.state.Frame(View{Width: 200, Height: 4}, r.now)
	if len(cut) != 3 || cut[0] != "├─ … and 2 more" {
		t.Fatalf("frame does not cut down to 4 rows:\n%s", strings.Join(cut, "\n"))
	}

	r.apply(
		`@nix {"action":"stop","id":2}`,
		`@nix {"action":"msg","level":0,"msg":"\u001b[31;1merror:\u001b[0m Cannot build '\u001b[35;1m`+drvPath("kernel")+`\u001b[0m'."}`,
	)
	got = r.state.Frame(View{Width: 200, Height: 40}, r.now)
	if !strings.Contains(got[1], ic.With(ic.Failed, "kernel")) || strings.Contains(strings.Join(got, "\n"), "unit") {
		t.Fatalf("frame after unit finished and kernel failed:\n%s", strings.Join(got, "\n"))
	}
}

func TestWaitingChainsCollapse(t *testing.T) {
	names := []string{"system", "etc", "units", "hm", "files", "path", "leaf"}
	inputs := map[string][]string{}
	for i := 0; i+1 < len(names); i++ {
		inputs[drvPath(names[i])] = []string{drvPath(names[i+1])}
	}
	r := &replay{t: t, now: epoch, state: NewState(Options{Live: true, Inputs: func(p string) ([]string, error) { return inputs[p], nil }}, epoch)}
	r.apply(`@nix {"action":"msg","level":3,"msg":"these 7 derivations will be built:"}`)
	for _, n := range names {
		r.apply(`@nix {"action":"msg","level":3,"msg":"  ` + drvPath(n) + `"}`)
	}
	r.apply(`@nix {"action":"start","id":1,"level":3,"type":105,"text":"","fields":["` + drvPath("leaf") + `","",1,1]}`)
	ic := ui.Unicode
	want := []string{
		"├─ " + ic.Planned + ic.Gap + "system",
		"│  ├─ … 4 more",
		"│  ╰─ " + ic.Planned + ic.Gap + "path",
		"│     ╰─ " + ic.Running + ic.Gap + "leaf  " + ic.With(ic.Clock, "0s"),
	}
	got := r.state.Frame(View{Width: 200, Height: 40}, r.now)
	if len(got) < len(want) || strings.Join(got[:len(want)], "\n") != strings.Join(want, "\n") {
		t.Fatalf("frame:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestEstimates(t *testing.T) {
	r := &replay{t: t, now: epoch, state: NewState(Options{Live: true, Estimates: map[string]time.Duration{"linux": 3 * time.Minute}}, epoch)}
	r.apply(
		`@nix {"action":"start","id":1,"level":3,"type":105,"text":"","fields":["`+drvPath("linux-6.18.63")+`","",1,1]}`,
		`@nix {"action":"start","id":2,"level":3,"type":105,"text":"","fields":["`+drvPath("unit-x.service")+`","",1,1]}`,
	)
	r.now = epoch.Add(65 * time.Second)
	ic := ui.Unicode
	frame := strings.Join(r.state.Frame(View{Width: 200, Height: 20}, r.now), "\n")
	if !strings.Contains(frame, "linux-6.18.63  "+ic.With(ic.Clock, "1m5s / 3m0s")) || !strings.Contains(frame, "unit-x.service  "+ic.With(ic.Clock, "1m5s")+"\n") {
		t.Fatalf("estimates not shown:\n%s", frame)
	}
	r.apply(`@nix {"action":"stop","id":1}`)
	if got := r.state.Measured(); len(got) != 1 || got["linux"] != 65*time.Second {
		t.Fatalf("Measured() = %v", got)
	}
}

func TestResizeRewindsReflowedFrame(t *testing.T) {
	var out bytes.Buffer
	now := epoch
	r := NewRenderer(&out, Config{Width: 80, Height: 20, Now: fixedClock(&now)})
	r.handle([]byte(`@nix {"action":"start","id":1,"level":3,"type":105,"text":"","fields":["` + drvPath(strings.Repeat("long-name-", 5)) + `","",1,1]}`))
	r.redraw()
	if len(r.widths) != 3 || r.widths[0] <= 40 {
		t.Fatalf("frame widths = %v", r.widths)
	}
	rows := 0
	for _, w := range r.widths {
		rows += (w + 19) / 20
	}
	r.flush()
	out.Reset()
	r.resize(ui.Size{Width: 20, Height: 20})
	r.redraw()
	r.flush()
	if want := syncStart + "\r\x1b[" + strconv.Itoa(rows-1) + "A"; !strings.HasPrefix(out.String(), want) {
		t.Fatalf("redraw after resize = %q, want prefix %q", out.String(), want)
	}
	for _, w := range r.widths {
		if w >= 20 {
			t.Fatalf("frame redrawn wider than the new terminal: %v", r.widths)
		}
	}
}

func TestRedrawOverwritesInPlace(t *testing.T) {
	var out bytes.Buffer
	now := epoch
	r := NewRenderer(&out, Config{Width: 80, Height: 20, Now: fixedClock(&now)})
	r.handle([]byte(`@nix {"action":"start","id":1,"level":3,"type":105,"text":"","fields":["` + drvPath("hello") + `","",1,1]}`))
	r.redraw()
	r.flush()
	out.Reset()
	now = now.Add(time.Second)
	r.redraw()
	r.flush()
	lines := r.state.Frame(r.cfg.view(), now)
	want := syncStart + "\r\x1b[" + strconv.Itoa(len(lines)-1) + "A" + strings.Join(lines, clearLine+"\n") + clearLine + clearToEnd + syncEnd
	if out.String() != want {
		t.Fatalf("redraw = %q, want %q", out.String(), want)
	}
}

func TestPrintedLineKeepsFullWidthCharacter(t *testing.T) {
	var out bytes.Buffer
	r := NewRenderer(&out, Config{Width: 10, Height: 20})
	r.print(strings.Repeat("x", 10))
	r.print("short")
	r.flush()
	want := syncStart + strings.Repeat("x", 10) + "\n" + "short" + clearLine + "\n" + syncEnd
	if out.String() != want {
		t.Fatalf("printed = %q, want %q", out.String(), want)
	}
}

func TestLiveWarningsAreFormattedOnce(t *testing.T) {
	var out bytes.Buffer
	r := NewRenderer(&out, Config{Width: 80, Height: 20})
	warning := `@nix {"action":"msg","level":1,"msg":"\u001b[35;1mwarning:\u001b[0m Git tree '/etc/nixos/dotfiles' is dirty"}`
	r.handle([]byte(warning))
	r.handle([]byte(warning))
	r.flush()
	want := ui.Unicode.Warn + " Git tree '/etc/nixos/dotfiles' is dirty"
	if got := strings.Count(out.String(), want); got != 1 {
		t.Fatalf("formatted warning printed %d times, want once:\n%q", got, out.String())
	}
	if strings.Contains(out.String(), "warning:") {
		t.Fatalf("raw warning prefix printed:\n%q", out.String())
	}
}

func TestScreenRows(t *testing.T) {
	if got := screenRows([]int{0, 20, 21, 45}, 20); got != 1+1+2+3 {
		t.Fatalf("screenRows = %d", got)
	}
}

func TestFrameBeforeAnyEvent(t *testing.T) {
	s := NewState(Options{Live: true}, epoch)
	got := s.Frame(View{Width: 80, Height: 20}, epoch.Add(3*time.Second))
	if want := "╺━ " + ui.Unicode.With(ui.Unicode.Clock, "3s"); len(got) != 1 || got[0] != want {
		t.Fatalf("frame while evaluating = %q, want %q", got, want)
	}
	if lines := s.Summary(ui.Style{}, epoch); lines != nil {
		t.Fatalf("summary printed for a build that did nothing: %q", lines)
	}
}

func TestParseInputDrvs(t *testing.T) {
	drv := `Derive([("out","/nix/store/sd779x86bjaqxj2lzh2jsir4h0zjz6d1-hello-2.12.3","","")],` +
		`[("/nix/store/08grahh6i022hkpbaz3yix4wvrcrpyks-stdenv-linux.drv",["out"]),("/nix/store/kbpcpb1ps9sxn55ng7yifk6zmncrjifx-bash-5.3p20.drv",["out","dev"])],` +
		`["/nix/store/l622p70vy8k5sh7y5wizi5f2mic6ynpg-source-stdenv.sh"],"x86_64-linux","/bin/sh",["-e","a \"quoted\" ] arg"],[("name","hello")])`
	got, err := ParseInputDrvs([]byte(drv))
	if err != nil {
		t.Fatal(err)
	}
	want := "/nix/store/08grahh6i022hkpbaz3yix4wvrcrpyks-stdenv-linux.drv /nix/store/kbpcpb1ps9sxn55ng7yifk6zmncrjifx-bash-5.3p20.drv"
	if strings.Join(got, " ") != want {
		t.Fatalf("ParseInputDrvs = %q", got)
	}
	for _, bad := range []string{"", "Derive(", `Derive([("out"`, `Derive([],[("/nix/store/x.drv"`, "{}"} {
		if _, err := ParseInputDrvs([]byte(bad)); err == nil {
			t.Errorf("ParseInputDrvs(%q) succeeded", bad)
		}
	}
}

func TestMalformedLineIsReported(t *testing.T) {
	var out bytes.Buffer
	err := NewRenderer(&out, Config{}).Consume(strings.NewReader("@nix {broken\nplain\n"))
	if err == nil {
		t.Fatal("Consume swallowed a malformed event")
	}
	if out.String() != "@nix {broken\nplain\n" {
		t.Fatalf("lines not passed through: %q", out.String())
	}
}

func TestUnreadableDerivationIsReported(t *testing.T) {
	var out bytes.Buffer
	cfg := Config{Inputs: ReadInputDrvs}
	in := `@nix {"action":"msg","level":3,"msg":"this derivation will be built:"}` + "\n" +
		`@nix {"action":"msg","level":3,"msg":"  /nix/store/` + strings.Repeat("z", 32) + `-missing.drv"}` + "\n"
	if err := NewRenderer(&out, cfg).Consume(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), "missing.drv") {
		t.Fatalf("Consume err = %v", err)
	}
}

func TestDownloadProgress(t *testing.T) {
	const mib = 1 << 20
	n := func(v int) string { return strconv.Itoa(v) }
	hello := "/nix/store/" + strings.Repeat("b", 32) + "-hello-2.12"
	tools := "/nix/store/" + strings.Repeat("c", 32) + "-tools-1.0"
	r := &replay{t: t, now: epoch, state: NewState(Options{Live: true}, epoch)}
	r.apply(
		`@nix {"action":"start","id":1,"level":0,"type":102,"text":""}`,
		`@nix {"action":"start","id":2,"level":0,"type":103,"text":""}`,
		`@nix {"action":"start","id":3,"level":0,"type":104,"text":""}`,
		`@nix {"action":"result","id":3,"type":105,"fields":[0,2,0,0]}`,
		`@nix {"action":"result","id":2,"type":105,"fields":[0,2,1,0]}`,
		`@nix {"action":"result","id":1,"type":106,"fields":[101,`+n(100*mib)+`]}`,
		`@nix {"action":"result","id":1,"type":106,"fields":[100,`+n(300*mib)+`]}`,
		`@nix {"action":"start","id":10,"level":3,"type":100,"text":"","fields":["`+hello+`","https://cache.nixos.org","local"]}`,
		`@nix {"action":"start","id":11,"level":4,"parent":10,"type":101,"text":"","fields":["https://cache.nixos.org/nar/x.nar.zst"]}`,
		`@nix {"action":"result","id":11,"type":105,"fields":[0,`+n(60*mib)+`,0,0]}`,
	)
	r.now = epoch.Add(3 * time.Second)
	r.apply(`@nix {"action":"result","id":11,"type":105,"fields":[` + n(30*mib) + `,` + n(60*mib) + `,0,0]}`)

	if got, want := r.state.Downloads(), (Downloads{Done: 30 * mib, Expected: 100 * mib, Unpacked: 300 * mib}); got != want {
		t.Fatalf("Downloads() = %+v, want %+v", got, want)
	}
	ic := ui.Unicode
	want := []string{
		"├─ " + ic.Download + ic.Gap + "hello-2.12  " + strings.Repeat("━", 10) + strings.Repeat("─", 10) + "  30.0 / 60.0 MiB  10.00 MiB/s  cache.nixos.org",
		"┝━ " + ic.Download + ic.Gap + strings.Repeat("━", 6) + strings.Repeat("─", 14) + "  30.0 / 100.0 MiB  10.00 MiB/s  ETA 7s",
		"┝━━━ Downloads",
		"┕━ " + ic.Summary + ic.Gap + ic.With(ic.Download, "1") + " " + ic.With(ic.Done, "0") + " " + ic.With(ic.Planned, "1") + " 30.00 MiB │ " + ic.With(ic.Clock, "3s"),
	}
	if got := r.state.Frame(View{Width: 200, Height: 20}, r.now); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("frame:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	r.apply(
		`@nix {"action":"result","id":11,"type":105,"fields":[`+n(60*mib)+`,`+n(60*mib)+`,0,0]}`,
		`@nix {"action":"stop","id":11}`,
		`@nix {"action":"result","id":10,"type":105,"fields":[`+n(50*mib)+`,`+n(200*mib)+`,0,0]}`,
	)
	unpacking := r.state.Frame(View{Width: 200, Height: 20}, r.now)
	if want := "hello-2.12  " + strings.Repeat("━", 5) + strings.Repeat("─", 15) + "  unpacking 50.0 / 200.0 MiB  cache.nixos.org"; !strings.HasSuffix(unpacking[0], want) {
		t.Fatalf("unpacking row = %q, want suffix %q", unpacking[0], want)
	}
	if got := r.state.Downloads().Done; got != 60*mib {
		t.Fatalf("finished transfer bytes not kept: Done = %d", got)
	}

	stalled := r.state.Frame(View{Width: 200, Height: 20}, r.now.Add(rateWindow))
	if strings.Contains(stalled[1], "/s") || strings.Contains(stalled[1], "ETA") {
		t.Fatalf("stalled download still shows a rate: %q", stalled[1])
	}

	r.apply(
		`@nix {"action":"stop","id":10}`,
		`@nix {"action":"start","id":20,"level":3,"type":100,"text":"","fields":["`+tools+`","https://xilos.dev/","local"]}`,
		`@nix {"action":"start","id":21,"level":4,"parent":20,"type":101,"text":"","fields":["https://xilos.dev/nar/y.nar.zst"]}`,
		`@nix {"action":"result","id":21,"type":105,"fields":[`+n(40*mib)+`,`+n(40*mib)+`,0,0]}`,
		`@nix {"action":"stop","id":21}`,
		`@nix {"action":"stop","id":20}`,
		`@nix {"action":"result","id":2,"type":105,"fields":[2,2,0,0]}`,
	)
	done := r.state.Frame(View{Width: 200, Height: 20}, r.now)
	if len(done) != 2 || strings.Contains(done[0], "ETA") {
		t.Fatalf("download row outlives the downloads:\n%s", strings.Join(done, "\n"))
	}
	summary := r.state.Summary(ui.Style{}, r.now)
	if want := "Finished at 12:00:03 after 3s: fetched 2 (60.00 MiB from cache.nixos.org, 40.00 MiB from xilos.dev)"; len(summary) != 1 || !strings.HasSuffix(summary[0], want) {
		t.Fatalf("summary = %q, want suffix %q", summary, want)
	}
}

func TestPhaseLabel(t *testing.T) {
	r := &replay{t: t, now: epoch, state: NewState(Options{Live: true}, epoch)}
	ic := ui.Unicode
	legend := func() string {
		frame := r.state.Frame(View{Width: 200, Height: 20}, r.now)
		return frame[len(frame)-1]
	}
	r.apply(`@nix {"action":"start","id":1,"level":4,"type":0,"text":"evaluating derivation 'flake:x#y'"}`)
	if got, want := legend(), "╺━ evaluating │ "+ic.With(ic.Clock, "0s"); got != want {
		t.Fatalf("legend while evaluating = %q, want %q", got, want)
	}
	r.apply(`@nix {"action":"start","id":2,"level":6,"type":0,"text":"querying info about missing paths"}`)
	if got := legend(); !strings.Contains(got, "evaluating") {
		t.Fatalf("evaluation does not take precedence over querying: %q", got)
	}
	r.apply(`@nix {"action":"stop","id":1}`)
	if got := legend(); !strings.Contains(got, "querying caches") {
		t.Fatalf("legend while querying = %q", got)
	}
	r.apply(`@nix {"action":"stop","id":2}`)
	if got, want := legend(), "╺━ "+ic.With(ic.Clock, "0s"); got != want {
		t.Fatalf("legend after evaluation = %q, want %q", got, want)
	}
}

func TestThroughputSamplesAreBounded(t *testing.T) {
	var tp throughput
	for i := range 10_000 {
		tp.record(epoch.Add(time.Duration(i)*10*time.Millisecond), int64(i)*1000)
	}
	if n := len(tp.samples); n > 2*int(rateWindow/sampleInterval) {
		t.Fatalf("throughput keeps %d samples", n)
	}
	now := epoch.Add(99_990 * time.Millisecond)
	if got := tp.perSecond(now); got < 99_000 || got > 101_000 {
		t.Fatalf("perSecond = %d, want about 100000", got)
	}
}

func TestStreamedDownloadIsEstimatedFromUnpack(t *testing.T) {
	const mib = 1 << 20
	n := func(v int) string { return strconv.Itoa(v) }
	path := "/nix/store/" + strings.Repeat("d", 32) + "-fortune-mod-3.26.0"
	r := &replay{t: t, now: epoch, state: NewState(Options{Live: true}, epoch)}
	r.apply(
		`@nix {"action":"start","id":10,"level":3,"type":100,"text":"","fields":["`+path+`","https://cache.nixos.org","local"]}`,
		`@nix {"action":"start","id":11,"level":4,"parent":10,"type":101,"text":"","fields":["https://cache.nixos.org/nar/z.nar.zst"]}`,
		`@nix {"action":"result","id":11,"type":105,"fields":[0,`+n(60*mib)+`,0,0]}`,
		`@nix {"action":"result","id":10,"type":105,"fields":[`+n(100*mib)+`,`+n(200*mib)+`,0,0]}`,
	)
	if got := r.state.Downloads().Done; got != 30*mib {
		t.Fatalf("in-flight download = %d, want half of the compressed size (%d) while half is unpacked", got, 30*mib)
	}
	r.apply(
		`@nix {"action":"result","id":11,"type":105,"fields":[`+n(61*mib)+`,`+n(61*mib)+`,0,0]}`,
		`@nix {"action":"stop","id":11}`,
	)
	if got := r.state.Downloads().Done; got != 61*mib {
		t.Fatalf("finished download = %d, want the reported %d", got, 61*mib)
	}
}
