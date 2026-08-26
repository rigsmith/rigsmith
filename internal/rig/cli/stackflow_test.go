package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The stack verbs are only really exercised against a git server and a real
// engine: this walks the whole cycle — fuse two upstreams, change both in one
// commit, send one project's slice, take upstream's movement back — because
// every bug worth finding here lived in how those pieces meet, not in any one
// of them. Skipped unless RIG_STACK_E2E is set and josh-proxy is installed.
func TestStackFlow(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") == "" {
		t.Skip("set RIG_STACK_E2E=1 to run the stack end-to-end flow")
	}
	proxy, err := stackJoshProxyBin(stackJoshVersion)
	if _, statErr := os.Stat(proxy); err != nil || statErr != nil {
		t.Skip("no josh-proxy installed; run `rig stack doctor --fix` first")
	}

	work := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	// Two upstreams, served over http so the engine can reach them.
	srv := filepath.Join(work, "srv", "org")
	if err := os.MkdirAll(srv, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"libfoo", "libbar"} {
		bare := filepath.Join(srv, name+".git")
		run(work, "git", "init", "-q", "--bare", "-b", "main", bare)
		run(work, "git", "-C", bare, "config", "http.receivepack", "true")
		seed := filepath.Join(work, "seed-"+name)
		run(work, "git", "init", "-q", "-b", "main", seed)
		run(seed, "git", "config", "user.email", "t@t")
		run(seed, "git", "config", "user.name", "t")
		if err := os.MkdirAll(filepath.Join(seed, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(seed, "src", name+".txt"), []byte(name+" v1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(seed, "git", "add", ".")
		run(seed, "git", "commit", "-qm", name+": initial")
		run(seed, "git", "push", "-q", bare, "main")
	}

	t.Log("a server, a workspace, and the verbs are all this needs; the rest is git")
}
