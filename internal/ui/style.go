package ui

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

type Attr string

const (
	Bold       Attr = "1"
	Faint      Attr = "2"
	Italic     Attr = "3"
	Red        Attr = "31"
	Green      Attr = "32"
	Yellow     Attr = "33"
	Blue       Attr = "34"
	Magenta    Attr = "35"
	Cyan       Attr = "36"
	Gray       Attr = "90"
	BrightCyan Attr = "96"
)

const (
	csi   = "\x1b["
	reset = csi + "0m"
)

type Style struct {
	color bool
	icons *Icons
}

func NewStyle(color bool, icons *Icons) Style {
	return Style{color: color, icons: icons}
}

func (s Style) Color() bool { return s.color }

func (s Style) Icons() *Icons {
	if s.icons == nil {
		return &Unicode
	}
	return s.icons
}

func (s Style) Paint(text string, attrs ...Attr) string {
	if !s.color || len(attrs) == 0 || text == "" {
		return text
	}
	var b strings.Builder
	b.Grow(len(text) + len(csi) + 3*len(attrs) + len(reset))
	b.WriteString(csi)
	for i, a := range attrs {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(string(a))
	}
	b.WriteByte('m')
	b.WriteString(text)
	b.WriteString(reset)
	return b.String()
}

func DetectColor(f *os.File, getenv func(string) string) bool {
	if getenv("NO_COLOR") != "" {
		return false
	}
	if getenv("FORCE_COLOR") != "" || getenv("CLICOLOR_FORCE") != "" {
		return true
	}
	if getenv("TERM") == "dumb" {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func Strip(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if n := escapeLen(s[i:]); n > 0 {
			i += n
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func Width(s string) int {
	return utf8.RuneCountInString(Strip(s))
}

func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if Width(s) <= width {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	visible := 0
	styled := false
	for i := 0; i < len(s); {
		if n := escapeLen(s[i:]); n > 0 {
			b.WriteString(s[i : i+n])
			styled = true
			i += n
			continue
		}
		if visible == width-1 {
			break
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		visible++
		i += size
	}
	b.WriteString("…")
	if styled {
		b.WriteString(reset)
	}
	return b.String()
}

func PadRight(s string, width int) string {
	if gap := width - Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

func escapeLen(s string) int {
	if !strings.HasPrefix(s, csi) {
		return 0
	}
	for i := len(csi); i < len(s); i++ {
		if c := s[i]; c >= 0x40 && c <= 0x7e {
			return i + 1
		}
	}
	return len(s)
}

const byteUnit = 1024

func Bytes(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	if n < byteUnit {
		return fmt.Sprintf("%s%d B", sign, n)
	}
	exp := byteExponent(n)
	return fmt.Sprintf("%s%.2f %s", sign, scaleBytes(n, exp), byteSuffix(exp))
}

func ByteProgress(done, total int64) string {
	if total < byteUnit {
		return fmt.Sprintf("%d / %d B", done, total)
	}
	exp := byteExponent(total)
	return fmt.Sprintf("%.1f / %.1f %s", scaleBytes(done, exp), scaleBytes(total, exp), byteSuffix(exp))
}

func byteExponent(n int64) int {
	exp := 0
	for value := float64(n) / byteUnit; value >= byteUnit && exp < 4; value /= byteUnit {
		exp++
	}
	return exp
}

func scaleBytes(n int64, exp int) float64 {
	value := float64(n) / byteUnit
	for range exp {
		value /= byteUnit
	}
	return value
}

func byteSuffix(exp int) string {
	return string("KMGTP"[exp]) + "iB"
}
