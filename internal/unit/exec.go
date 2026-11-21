package unit

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type Command struct {
	Path          string
	Argv0         string
	Args          []string
	IgnoreFailure bool
	ExpandEnv     bool
}

func (c Command) String() string {
	return strings.Join(append([]string{c.Path}, c.Args...), " ")
}

func parseCommands(value string, spec specifiers) ([]Command, error) {
	words, err := extractWords(value)
	if err != nil {
		return nil, err
	}
	var (
		out   []Command
		group []word
	)
	for _, w := range words {
		if w.bare && w.text == ";" {
			cmd, err := parseCommand(group, spec)
			if err != nil {
				return nil, err
			}
			out = append(out, cmd)
			group = nil
			continue
		}
		group = append(group, w)
	}
	if len(group) > 0 || len(out) == 0 {
		cmd, err := parseCommand(group, spec)
		if err != nil {
			return nil, err
		}
		out = append(out, cmd)
	}
	return out, nil
}

func parseCommand(words []word, spec specifiers) (Command, error) {
	if len(words) == 0 {
		return Command{}, errors.New("empty command")
	}
	cmd := Command{ExpandEnv: true}
	first := words[0].text
	argv0 := false
prefixes:
	for first != "" {
		switch first[0] {
		case '-':
			cmd.IgnoreFailure = true
		case '@':
			argv0 = true
		case ':':
			cmd.ExpandEnv = false
		case '+', '!':
		case '|':
			return Command{}, fmt.Errorf("%q: the | (login shell) prefix is not supported", words[0].text)
		default:
			break prefixes
		}
		first = first[1:]
	}
	if first == "" {
		return Command{}, fmt.Errorf("%q: no executable after the prefixes", words[0].text)
	}
	path, err := spec.expand(first)
	if err != nil {
		return Command{}, err
	}
	if !filepath.IsAbs(path) && strings.ContainsRune(path, '/') {
		return Command{}, fmt.Errorf("executable %q must be an absolute path or a bare name", path)
	}
	cmd.Path, cmd.Argv0 = path, path
	rest := words[1:]
	if argv0 {
		if len(rest) == 0 {
			return Command{}, fmt.Errorf("%q: the @ prefix needs an argv[0] after the executable", path)
		}
		if cmd.Argv0, err = spec.expand(rest[0].text); err != nil {
			return Command{}, err
		}
		rest = rest[1:]
	}
	cmd.Args = make([]string, len(rest))
	for i, w := range rest {
		if cmd.Args[i], err = spec.expand(w.text); err != nil {
			return Command{}, err
		}
	}
	return cmd, nil
}

func (c Command) Argv(lookup func(string) string) []string {
	argv := make([]string, 0, len(c.Args)+1)
	argv = append(argv, c.Argv0)
	if !c.ExpandEnv {
		return append(argv, c.Args...)
	}
	for _, a := range c.Args {
		if name, ok := wholeVariable(a); ok {
			argv = append(argv, strings.Fields(lookup(name))...)
			continue
		}
		argv = append(argv, expandBraced(a, lookup))
	}
	return argv
}

func wholeVariable(s string) (string, bool) {
	if len(s) < 2 || s[0] != '$' || s[1] == '{' || s[1] == '$' {
		return "", false
	}
	name := s[1:]
	if !validEnvName(name) {
		return "", false
	}
	return name, true
}

func expandBraced(s string, lookup func(string) string) string {
	if !strings.ContainsRune(s, '$') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case '$':
			b.WriteByte('$')
			i++
		case '{':
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 || !validEnvName(s[i+2:i+2+end]) {
				b.WriteByte('$')
				continue
			}
			b.WriteString(lookup(s[i+2 : i+2+end]))
			i += 2 + end
		default:
			b.WriteByte('$')
		}
	}
	return b.String()
}

func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c == '_', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
