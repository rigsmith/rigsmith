package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
)

func TestE2E_CLIStoreCoordination(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") != "1" {
		t.Skip("gated: CLAUDERIG_E2E=1")
	}
	_, file, _, _ := runtime.Caller(0)
	bin := filepath.Join(t.TempDir(), "clauderig")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "./cmd/clauderig")
	build.Dir = filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME"} {
		t.Setenv(key, home)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	fixtureGit(t)
	cfg := cliOnly(filepath.Join(home, ".claude"))
	cfg.HookIntervalMinutes = new(int)
	me := config.Detect("fixture")
	cfg.Machines[me.Name] = me
	must(t, os.MkdirAll(filepath.Join(home, ".clauderig"), 0o755))
	must(t, config.Save(cfg, filepath.Join(home, ".clauderig")))
	write(t, home, ".claude/settings.json", `{"theme":"before"}`)
	cwd := filepath.Join(home, "Git", "acme")
	write(t, home, ".claude/projects/"+project.Flatten(cwd)+"/s.jsonl", `{"type":"user","cwd":"`+jsonEsc(cwd)+`","isSidechain":false}`+"\n")

	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = home
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("sync", "--flush")
	staging, err := config.StagingDir()
	must(t, err)
	before := read(t, filepath.Join(staging, "cli/settings.json"))
	_, release, err := storelock.Acquire(t.Context(), staging, 0)
	must(t, err)
	defer release()
	write(t, home, ".claude/settings.json", `{"theme":"after"}`)
	for _, args := range [][]string{{"sync", "--hook"}, {"pull"}} {
		if out := run(args...); !strings.Contains(out, "staging store") {
			t.Fatalf("busy hook did not report coordination: %s", out)
		}
	}
	if read(t, filepath.Join(staging, "cli/settings.json")) != before {
		t.Fatal("busy hook changed staging")
	}
	release()
	run("sync", "--flush")
	target := filepath.Join(home, "restored")
	run("restore", "--dir", target)
	if got := read(t, filepath.Join(target, "settings.json")); !strings.Contains(got, "after") {
		t.Fatalf("sync/restore did not recover after contention: %s", got)
	}
}
