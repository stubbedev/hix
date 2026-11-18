package runlock

import (
	"errors"
	"os"
	"strconv"
	"testing"
)

func TestSecondAcquireFailsWithTheOwner(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Acquire(dir)
	var held *HeldError
	if !errors.As(err, &held) || held.Owner != strconv.Itoa(os.Getpid()) {
		t.Fatalf("second Acquire = %v, want a HeldError naming pid %d", err, os.Getpid())
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	if err := again.Release(); err != nil {
		t.Fatal(err)
	}
}
