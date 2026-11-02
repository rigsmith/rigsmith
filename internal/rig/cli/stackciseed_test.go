package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStackCISeedValidation(t *testing.T) {
	for _, args := range [][]string{nil, {"--manifest", "x", "--seed", "acme/stack"}} {
		c := newStackCICmd()
		c.SetArgs(args)
		c.SetOut(&bytes.Buffer{})
		c.SetErr(&bytes.Buffer{})
		err := c.Execute()
		code, ok := StackCIExitCode(err)
		if !ok || code != 10 || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("%v: %v", args, err)
		}
	}
	secret := "do-not-print-this-token"
	seed := "https://you:" + secret + "@blocked.invalid/acme/stack.git"
	_, _, err := stackCISeed(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "seed"), seed, "github.com")
	if err == nil || !strings.Contains(err.Error(), "credentials") || strings.Contains(err.Error(), secret) {
		t.Fatal(err)
	}
	seed = "https://blocked.invalid/acme/stack.git"
	_, _, err = stackCISeed(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "seed"), seed, "github.com")
	if err == nil || !strings.Contains(err.Error(), "--allowed-hosts") {
		t.Fatal(err)
	}
	// A local URL rewrite makes the transport failure deterministic and offline.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file:///missing-seed/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://blocked.invalid/")
	_, _, err = stackCISeed(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "seed"), seed, "blocked.invalid")
	if err == nil || !strings.Contains(err.Error(), "fork PRs") || strings.Contains(err.Error(), secret) {
		t.Fatal(err)
	}
}

func TestStackCIActionSeed(t *testing.T) {
	for _, tc := range []struct {
		manifest, seed string
		valid          bool
	}{
		{"", "", false}, {"manifest", "acme/seed@main", false}, {"", "acme/seed@main", true}, {"manifest", "", true},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "rig"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0755); err != nil {
			t.Fatal(err)
		}
		p := exec.Command("bash", "-euo", "pipefail", "-c", stackCIActionScript(t, "Rebuild, wire, build and test"))
		p.Env = append(stackCIActionDefaultEnv(t), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "STACK_FORK_PR=false", "STACK_MANIFEST="+tc.manifest, "STACK_SEED="+tc.seed, "RIG_STACK_CACHE_REPORT="+filepath.Join(dir, "report"), "RIG_STACK_PROVENANCE="+filepath.Join(dir, "provenance"))
		out, err := p.CombinedOutput()
		if (err == nil) != tc.valid {
			t.Fatalf("%+v: %s %v", tc, out, err)
		}
		if tc.valid && tc.seed != "" && !strings.Contains(string(out), "--seed\n"+tc.seed) {
			t.Fatalf("seed not passed: %s", out)
		}
	}
}
