package supervisor

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stubbedev/phix/internal/unit"
)

const (
	maxLogSize     = 1 << 20
	maxLineLength  = 16 << 10
	subscriberSlot = 256
	timeLayout     = "2006-01-02T15:04:05.000Z07:00"
)

type Journal struct {
	dir  string
	now  func() time.Time
	mu   sync.Mutex
	subs map[string]map[chan string]struct{}
}

func NewJournal(dir string, now func() time.Time) *Journal {
	return &Journal{dir: dir, now: now, subs: map[string]map[chan string]struct{}{}}
}

func (j *Journal) path(name string) string { return filepath.Join(j.dir, name+".log") }

func (j *Journal) Append(name, text string) error {
	line := j.now().UTC().Format(timeLayout) + " " + text
	j.mu.Lock()
	defer j.mu.Unlock()
	path := j.path(name)
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogSize {
		if err := os.Rename(path, path+".1"); err != nil {
			return fmt.Errorf("rotating %s: %w", path, err)
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	_, werr := f.WriteString(line + "\n")
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	for ch := range j.subs[name] {
		select {
		case ch <- line:
		default:
			delete(j.subs[name], ch)
			close(ch)
		}
	}
	return nil
}

func LogName(units []string) (string, error) {
	if len(units) == 0 {
		return SupervisorLog, nil
	}
	n, err := unit.ParseServiceArg(units[0])
	if err != nil {
		return "", err
	}
	return n.String(), nil
}

func ReadLog(l Layout, name string, n int) ([]string, error) {
	return NewJournal(l.LogDir(), time.Now).Tail(name, n)
}

func (j *Journal) Tail(name string, n int) ([]string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.tailLocked(name, n)
}

func (j *Journal) tailLocked(name string, n int) ([]string, error) {
	var lines []string
	for _, p := range []string{j.path(name) + ".1", j.path(name)} {
		data, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		for line := range bytes.SplitSeq(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'}) {
			if len(line) > 0 {
				lines = append(lines, string(line))
			}
		}
	}
	if n >= 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

func (j *Journal) Follow(name string, n int) ([]string, <-chan string, func(), error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	lines, err := j.tailLocked(name, n)
	if err != nil {
		return nil, nil, nil, err
	}
	ch := make(chan string, subscriberSlot)
	if j.subs[name] == nil {
		j.subs[name] = map[chan string]struct{}{}
	}
	j.subs[name][ch] = struct{}{}
	cancel := func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if _, ok := j.subs[name][ch]; ok {
			delete(j.subs[name], ch)
			close(ch)
		}
	}
	return lines, ch, cancel, nil
}

func (j *Journal) Pump(name string, r io.Reader, report func(error)) {
	br := bufio.NewReaderSize(r, maxLineLength)
	for {
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 {
			if aerr := j.Append(name, string(bytes.TrimRight(chunk, "\r\n"))); aerr != nil {
				report(aerr)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
			report(fmt.Errorf("reading the output of %s: %w", name, err))
		}
		if err != nil {
			return
		}
	}
}
