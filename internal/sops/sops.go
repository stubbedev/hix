package sops

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/stubbedev/phix/internal/agekey"
	"github.com/stubbedev/phix/internal/atomicfile"
	"github.com/stubbedev/phix/internal/proc"
	"github.com/stubbedev/phix/internal/ui"
	"github.com/stubbedev/phix/internal/usage"
)

const (
	autoNameSuffix = 8
	secretPerm     = 0o644
)

var (
	secretNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	trustNamePattern  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	binaryInput       = []string{"--input-type", "binary"}
	binaryMode        = append(slices.Clone(binaryInput), "--output-type", "binary")
)

type Store struct {
	Flake   string
	Console ui.Console
}

func (s Store) configPath() string { return filepath.Join(s.Flake, ".sops.yaml") }

func (s Store) secretsDir() string { return filepath.Join(s.Flake, "secrets") }

func (s Store) sops(args ...string) proc.Cmd {
	return s.Console.Cmd(append([]string{"sops", "--config", s.configPath()}, args...)...)
}

func ValidateSecretName(name string) error {
	if !secretNamePattern.MatchString(name) {
		return usage.Errorf("secret name %q must match %s (a file directly under secrets/)", name, secretNamePattern)
	}
	return nil
}

func (s Store) ResolveSecret(name string) (string, error) {
	if err := ValidateSecretName(name); err != nil {
		return "", err
	}
	path := filepath.Join(s.secretsDir(), name)
	if _, err := os.Lstat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("looking up secret %s: %w", name, err)
	}
	hits, err := filepath.Glob(filepath.Join(s.secretsDir(), name+".*"))
	if err != nil {
		return "", fmt.Errorf("looking up secret %s: %w", name, err)
	}
	switch len(hits) {
	case 0:
		return path, nil
	case 1:
		return hits[0], nil
	}
	names := make([]string, len(hits))
	for i, h := range hits {
		names[i] = filepath.Base(h)
	}
	return "", usage.Errorf("secret %q is ambiguous: %s", name, strings.Join(names, " "))
}

func (s Store) SecretNames() ([]string, error) {
	entries, err := os.ReadDir(s.secretsDir())
	if err != nil {
		return nil, fmt.Errorf("listing secrets: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

type MissingSecretError struct {
	Name string
}

func (e *MissingSecretError) Error() string { return "secrets/" + e.Name + " is missing" }

func (s Store) Decrypt(ctx context.Context, name string) ([]byte, error) {
	path, err := s.ResolveSecret(name)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, &MissingSecretError{Name: name}
	} else if err != nil {
		return nil, fmt.Errorf("decrypting secrets/%s: %w", name, err)
	}
	out, err := proc.OutputQuiet(ctx, s.sops(append([]string{"--decrypt"}, append(slices.Clone(binaryMode), path)...)...))
	if err != nil {
		return nil, fmt.Errorf("decrypting secrets/%s: %w", name, err)
	}
	return bytes.TrimRight(out, "\n"), nil
}

func (s Store) Edit(ctx context.Context, path string) error {
	return proc.Run(ctx, s.sops(append(slices.Clone(binaryMode), path)...))
}

func (s Store) Set(ctx context.Context, path string, value []byte) error {
	cmd := s.sops(append([]string{"--encrypt"}, append(slices.Clone(binaryMode), "--filename-override", path, "/dev/stdin")...)...)
	cmd.Stdin = bytes.NewReader(value)
	return atomicfile.Replace(path, secretPerm, func(w io.Writer) error {
		cmd.Stdout = w
		if err := proc.RunQuiet(ctx, cmd); err != nil {
			return fmt.Errorf("encrypting %s: %w", path, err)
		}
		return nil
	})
}

func (s Store) Rotate(ctx context.Context, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("rotating %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("rotating %s: not a regular file", path)
	}
	return s.Console.RunIndented(ctx, s.sops(append([]string{"--rotate", "-i"}, append(slices.Clone(binaryMode), path)...)...), sopsFormat)
}

func ParsePipedValue(data []byte) ([]byte, error) {
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("value contains a NUL byte")
	}
	value := bytes.TrimRight(data, "\n")
	if len(value) == 0 {
		return nil, errors.New("refusing to write an empty value")
	}
	return value, nil
}

func AgePubkey(sshPublicKey string) (string, error) {
	data, err := os.ReadFile(sshPublicKey)
	if err != nil {
		return "", fmt.Errorf("deriving an age pubkey: %w", err)
	}
	recipient, err := agekey.FromSSHPublicKey(data)
	if err != nil {
		return "", fmt.Errorf("deriving an age pubkey from %s: %w", sshPublicKey, err)
	}
	return string(recipient), nil
}

type TrustRequest struct {
	Name   string
	Pubkey string
}

func ParseTrustArgs(args []string, stdin io.Reader, stdinTTY bool) (TrustRequest, error) {
	switch len(args) {
	case 2:
		return TrustRequest{Name: args[0], Pubkey: args[1]}, nil
	case 1:
		return TrustRequest{Pubkey: args[0]}, nil
	case 0:
		if stdinTTY {
			return TrustRequest{}, usage.Errorf("trust needs a pubkey: phix trust [name] <pubkey>, or pipe '<name> <pubkey>' in (e.g. ssh other phix whoami | phix trust)")
		}
	default:
		return TrustRequest{}, usage.Errorf("trust takes at most two arguments, got %d", len(args))
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return TrustRequest{}, fmt.Errorf("reading the recipient from stdin: %w", err)
	}
	fields := strings.Fields(line)
	switch len(fields) {
	case 0:
		return TrustRequest{}, usage.Errorf("trust: empty stdin (expected '<name> <pubkey>' or '<pubkey>')")
	case 1:
		return TrustRequest{Pubkey: fields[0]}, nil
	}
	return TrustRequest{Name: fields[0], Pubkey: fields[1]}, nil
}

func AutoName(pubkey, localPubkey, hostname string) string {
	if localPubkey != "" && pubkey == localPubkey {
		return hostname
	}
	if len(pubkey) <= autoNameSuffix {
		return pubkey
	}
	return pubkey[len(pubkey)-autoNameSuffix:]
}

func ValidateTrustName(name string) error {
	if !trustNamePattern.MatchString(name) {
		return usage.Errorf("trust: name %q must match %s", name, trustNamePattern)
	}
	return nil
}

func (s Store) Trust(ctx context.Context, req TrustRequest) error {
	if err := ValidateTrustName(req.Name); err != nil {
		return err
	}
	if _, err := agekey.ParseRecipient(req.Pubkey); err != nil {
		return usage.Errorf("trust: %v", err)
	}
	names, err := s.SecretNames()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("trust: %w", err)
	}
	paths := make([]string, 0, len(names)+1)
	paths = append(paths, s.configPath())
	for _, name := range names {
		paths = append(paths, filepath.Join(s.secretsDir(), name))
	}
	saved, err := snapshot(paths)
	if err != nil {
		return fmt.Errorf("trust: %w", err)
	}
	updated, added, err := AddRecipient(saved[0].data, req.Pubkey, req.Name)
	if err != nil {
		return fmt.Errorf("trust: adding %s to %s: %w", req.Pubkey, s.configPath(), err)
	}
	if !added {
		s.Console.Out().Done("%s already present in .sops.yaml, nothing to do", req.Pubkey)
		return nil
	}
	if err := atomicfile.WriteKeepingMode(s.configPath(), updated); err != nil {
		return fmt.Errorf("trust: %w", err)
	}
	out := s.Console.Out()
	out.Done("Added %s → %s to .sops.yaml", out.Style.Paint(req.Name, ui.Bold), req.Pubkey)

	if err := s.rewrap(ctx, saved[1:]); err != nil {
		if restoreErr := restore(saved); restoreErr != nil {
			return fmt.Errorf("trust: %w; rolling back also failed: %w", err, restoreErr)
		}
		s.Console.Log().Warn("trust: rolled back .sops.yaml and secrets/")
		return fmt.Errorf("trust: %w", err)
	}

	out.Done("Trusted %s across %d secret file(s)", out.Style.Paint(req.Name, ui.Bold), len(names))
	command := func(format string, args ...any) string { return out.Style.Paint(fmt.Sprintf(format, args...), ui.Cyan) }
	fmt.Fprintf(out.W, "  %s  %s\n", out.Style.Paint("Review:", ui.Bold), command("git -C %q diff -- .sops.yaml secrets/", s.Flake))
	fmt.Fprintf(out.W, "  %s  %s\n", out.Style.Paint("Commit:", ui.Bold), command("git -C %q add .sops.yaml secrets/ && \\", s.Flake))
	fmt.Fprintf(out.W, "           %s\n", command("git -C %q commit -m \"trust: add %s age recipient\"", s.Flake, req.Name))
	return nil
}

type savedFile struct {
	path string
	data []byte
}

func snapshot(paths []string) ([]savedFile, error) {
	saved := make([]savedFile, len(paths))
	for i, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		saved[i] = savedFile{path: path, data: data}
	}
	return saved, nil
}

func restore(saved []savedFile) error {
	var errs []error
	for _, f := range saved {
		if err := atomicfile.WriteKeepingMode(f.path, f.data); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s Store) rewrap(ctx context.Context, secrets []savedFile) error {
	for _, f := range secrets {
		s.Console.Out().Step("Re-wrapping %s", filepath.Base(f.path))
		if err := s.Console.RunIndented(ctx, s.sops(append([]string{"updatekeys", "--yes"}, append(slices.Clone(binaryInput), f.path)...)...), sopsFormat); err != nil {
			return fmt.Errorf("re-wrapping %s: %w", filepath.Base(f.path), err)
		}
	}
	return nil
}
