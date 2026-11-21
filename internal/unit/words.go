package unit

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type word struct {
	text string
	bare bool
}

func splitWords(s string) ([]string, error) {
	words, err := extractWords(s)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = w.text
	}
	return out, nil
}

func extractWords(s string) ([]word, error) {
	var (
		words  []word
		cur    strings.Builder
		inWord bool
		bare   = true
		quote  byte
	)
	flush := func() {
		if inWord {
			words = append(words, word{text: cur.String(), bare: bare})
		}
		cur.Reset()
		inWord, bare = false, true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case c == '\\':
			r, n, err := unescape(s[i+1:])
			if err != nil {
				return nil, fmt.Errorf("%q: %w", s, err)
			}
			cur.WriteString(r)
			i += n
			inWord, bare = true, false
		case quote != 0:
			cur.WriteByte(c)
		case c == '"' || c == '\'':
			quote = c
			inWord, bare = true, false
		case isSpace(c):
			flush()
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("%q: unterminated %c quote", s, quote)
	}
	flush()
	return words, nil
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func unescape(s string) (string, int, error) {
	if s == "" {
		return "", 0, errors.New("trailing backslash")
	}
	switch c := s[0]; c {
	case 'a':
		return "\a", 1, nil
	case 'b':
		return "\b", 1, nil
	case 'f':
		return "\f", 1, nil
	case 'n':
		return "\n", 1, nil
	case 'r':
		return "\r", 1, nil
	case 't':
		return "\t", 1, nil
	case 'v':
		return "\v", 1, nil
	case 's':
		return " ", 1, nil
	case '\\', '"', '\'', ' ', ';', '$':
		return string(c), 1, nil
	case 'x':
		return codePoint(s, 1, 2, 16, true)
	case 'u':
		return codePoint(s, 1, 4, 16, false)
	case 'U':
		return codePoint(s, 1, 8, 16, false)
	case '0', '1', '2', '3', '4', '5', '6', '7':
		return codePoint(s, 0, 3, 8, true)
	}
	return "", 0, fmt.Errorf("unknown escape \\%c", s[0])
}

func codePoint(s string, skip, digits, base int, asByte bool) (string, int, error) {
	end := skip + digits
	if len(s) < end {
		return "", 0, fmt.Errorf("escape \\%s needs %d digits", s, digits)
	}
	n, err := strconv.ParseUint(s[skip:end], base, 32)
	if err != nil {
		return "", 0, fmt.Errorf("escape \\%s: %w", s[:end], err)
	}
	if n == 0 {
		return "", 0, fmt.Errorf("escape \\%s encodes a NUL byte", s[:end])
	}
	if asByte {
		if n > 0xff {
			return "", 0, fmt.Errorf("escape \\%s is above 0xff", s[:end])
		}
		return string([]byte{byte(n)}), end, nil
	}
	if n > unicode.MaxRune || !utf8.ValidRune(rune(n)) {
		return "", 0, fmt.Errorf("escape \\%s is not a valid code point", s[:end])
	}
	return string(rune(n)), end, nil
}
