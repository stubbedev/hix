package nixmcp

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

func safeGo(wg *sync.WaitGroup, fn func()) {
	wg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				logf("recovered panic in goroutine: %v", r)
			}
		}()
		fn()
	})
}

func parseISOTime(s string) (time.Time, bool) {
	s = strings.Replace(strings.TrimSpace(s), "Z", "+00:00", 1)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func errMsg(msg string) string         { return errCode("ERROR", msg) }
func errCode(code, msg string) string  { return fmt.Sprintf("Error (%s): %s", code, msg) }
func errorf(f string, a ...any) string { return errMsg(fmt.Sprintf(f, a...)) }

func parseIntStrict(s string) (int, error) { return strconv.Atoi(strings.TrimSpace(s)) }

func comma[T int | int64](n T) string {
	s := strconv.FormatInt(int64(n), 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

type narInfo struct {
	fileSize    int64
	narSize     int64
	compression string
	storePath   string
	url         string
	hasFileSize bool
	hasNarSize  bool
}

func parseNarInfo(text string) narInfo {
	var r narInfo
	for line := range strings.SplitSeq(text, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		switch k {
		case "filesize":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				r.fileSize, r.hasFileSize = n, true
			}
		case "narsize":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				r.narSize, r.hasNarSize = n, true
			}
		case "compression":
			r.compression = v
		case "storepath":
			r.storePath = v
		case "url":
			r.url = v
		}
	}
	return r
}

func validateStorePath(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = filepath.Clean(path)
	}
	return strings.HasPrefix(resolved, "/nix/store/")
}

func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	return bytes.IndexByte(buf[:n], 0) >= 0
}

func readFileWithLimit(path string, limit int) ([]string, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	all := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	if n := len(all); n > 0 && all[n-1] == "" {
		all = all[:n-1]
	}
	total := len(all)
	if total > limit {
		return all[:limit], total, nil
	}
	return all, total, nil
}

func stripHTML(s string) string {
	if s == "" {
		return ""
	}
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return strings.Join(strings.Fields(s), " ")
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return strings.Join(strings.Fields(b.String()), " ")
}
