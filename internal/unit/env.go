package unit

import (
	"bufio"
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"
)

type Var struct {
	Name  string
	Value string
}

type Env map[string]string

func EnvFrom(environ []string) Env {
	env := make(Env, len(environ))
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok && name != "" {
			env[name] = value
		}
	}
	return env
}

func (e Env) Clone() Env { return maps.Clone(e) }

func (e Env) Apply(vars []Var) {
	for _, v := range vars {
		e[v.Name] = v.Value
	}
}

func (e Env) Lookup(name string) string { return e[name] }

func (e Env) Environ() []string {
	out := make([]string, 0, len(e))
	for _, name := range slices.Sorted(maps.Keys(e)) {
		out = append(out, name+"="+e[name])
	}
	return out
}

func parseAssignment(s string) (Var, error) {
	name, value, ok := strings.Cut(s, "=")
	if !ok || !validEnvName(name) {
		return Var{}, fmt.Errorf("%q is not a NAME=VALUE assignment", s)
	}
	return Var{Name: name, Value: value}, nil
}

func ParseAssignmentList(value string) ([]Var, error) {
	return assignments(value, func(s string) (string, error) { return s, nil })
}

func parseAssignments(value string, spec specifiers) ([]Var, error) {
	return assignments(value, spec.expand)
}

func assignments(value string, expand func(string) (string, error)) ([]Var, error) {
	words, err := splitWords(value)
	if err != nil {
		return nil, err
	}
	out := make([]Var, 0, len(words))
	for _, w := range words {
		expanded, err := expand(w)
		if err != nil {
			return nil, err
		}
		v, err := parseAssignment(expanded)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func ParseEnvironmentFile(data []byte) ([]Var, error) {
	var out []Var
	lines := logicalLines(data)
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		name, raw, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !validEnvName(name) {
			return nil, fmt.Errorf("line %d: %q is not a NAME=VALUE assignment", i+1, line)
		}
		value, err := envFileValue(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		out = append(out, Var{Name: name, Value: value})
	}
	return out, nil
}

func logicalLines(data []byte) []string {
	var (
		out     []string
		pending strings.Builder
	)
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasSuffix(line, "\\") && !strings.HasSuffix(line, "\\\\") {
			pending.WriteString(strings.TrimSuffix(line, "\\"))
			continue
		}
		pending.WriteString(line)
		out = append(out, pending.String())
		pending.Reset()
	}
	if pending.Len() > 0 {
		out = append(out, pending.String())
	}
	return out
}

func envFileValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw[0] != '"' && raw[0] != '\'' {
		return raw, nil
	}
	words, err := splitWords(raw)
	if err != nil {
		return "", err
	}
	if len(words) != 1 {
		return "", fmt.Errorf("value %s has text after its closing quote", raw)
	}
	return words[0], nil
}

func ParseEnvironmentD(data []byte, env Env) error {
	for i, line := range logicalLines(data) {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		v, err := parseAssignment(line)
		if err != nil {
			return fmt.Errorf("line %d: %w", i+1, err)
		}
		env[v.Name] = expandShellStyle(v.Value, env)
	}
	return nil
}

func expandShellStyle(s string, env Env) string {
	if !strings.ContainsRune(s, '$') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		if s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				b.WriteString(s[i:])
				break
			}
			b.WriteString(expandBraceExpr(s[i+2:i+2+end], env))
			i += 2 + end
			continue
		}
		j := i + 1
		for j < len(s) && (s[j] == '_' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= 'a' && s[j] <= 'z' || j > i+1 && s[j] >= '0' && s[j] <= '9') {
			j++
		}
		if j == i+1 {
			b.WriteByte('$')
			continue
		}
		b.WriteString(env[s[i+1:j]])
		i = j - 1
	}
	return b.String()
}

func expandBraceExpr(expr string, env Env) string {
	if name, alt, ok := strings.Cut(expr, ":-"); ok {
		if v := env[name]; v != "" {
			return v
		}
		return expandShellStyle(alt, env)
	}
	if name, alt, ok := strings.Cut(expr, ":+"); ok {
		if env[name] != "" {
			return expandShellStyle(alt, env)
		}
		return ""
	}
	return env[expr]
}
