package main

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func TestRigExitStatusWrappedChild(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestRigExitStatusChild$")
	child.Env = append(os.Environ(), "RIG_EXIT_STATUS_CHILD=1")
	err = child.Run()
	if _, ok := err.(*exec.ExitError); !ok {
		t.Fatalf("want child exit error: %v", err)
	}
	if got := exitStatus(fmt.Errorf("another verb failed: %w", err)); got != 1 {
		t.Fatalf("wrapped child exit propagated: %d", got)
	}
}

func TestRigExitStatusChild(t *testing.T) {
	if os.Getenv("RIG_EXIT_STATUS_CHILD") == "1" {
		os.Exit(37)
	}
}
