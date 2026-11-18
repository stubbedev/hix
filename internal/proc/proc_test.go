package proc

import (
	"context"
	"strings"
	"testing"
)

func TestRunQuietFoldsStderrIntoTheError(t *testing.T) {
	err := RunQuiet(context.Background(), Cmd{Argv: []string{"sh", "-c", "echo boom >&2; exit 3"}})
	if err == nil || !strings.HasSuffix(err.Error(), ": boom") {
		t.Fatalf("RunQuiet error = %v, want the stderr text appended", err)
	}
}

func TestOutputQuietKeepsStdout(t *testing.T) {
	out, err := OutputQuiet(context.Background(), Cmd{Argv: []string{"sh", "-c", "echo out; echo noise >&2"}})
	if err != nil || string(out) != "out\n" {
		t.Fatalf("OutputQuiet = %q, %v", out, err)
	}
}
