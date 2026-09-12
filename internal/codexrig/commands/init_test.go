package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/codexrig/config"
)

// `init --yes` and `init --remote` skip the form, and the sessions flag's
// default is false — so assigning it on every path turned session syncing OFF
// for anyone who had it on and re-ran init for any other reason.
func TestInitDoesNotClobberASettingItNeverAskedAbout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.SyncSessions = true
	if err := config.Save(cfg, dir); err != nil {
		t.Fatal(err)
	}

	cmd := NewInitCmd()
	cmd.SetArgs([]string{"--yes"})
	var summary bytes.Buffer
	cmd.SetOut(&summary)
	cmd.SetErr(testWriter{t})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// And the summary reports what was saved, not the flag's default.
	if strings.Contains(summary.String(), "not session rollouts") {
		t.Errorf("summary says sessions are off while saving them on:\n%s", summary.String())
	}
	got, err := config.LoadOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	if !got.SyncSessions {
		t.Fatal("init --yes turned syncSessions off without asking")
	}

	// And the flag, when actually given, is honoured.
	cmd = NewInitCmd()
	cmd.SetArgs([]string{"--yes", "--sessions=false"})
	cmd.SetOut(testWriter{t})
	cmd.SetErr(testWriter{t})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, _ := config.LoadOrDefault(); got.SyncSessions {
		t.Fatal("an explicit --sessions=false was ignored")
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) { w.t.Log(string(b)); return len(b), nil }

// Detect knows the host, not the config. A re-run of init replaced the machine
// entry wholesale, and the custom folder tokens — configuration nothing else
// in the tool writes — went with it.
func TestInitKeepsAMachinesConfiguredTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	me := config.Detect("box")
	me.Tokens = map[string]string{"PROJECTS": "/srv/projects"}
	cfg.Machines["box"] = me
	if err := config.Save(cfg, dir); err != nil {
		t.Fatal(err)
	}
	cmd := NewInitCmd()
	cmd.SetArgs([]string{"--yes", "--name", "box"})
	cmd.SetOut(testWriter{t})
	cmd.SetErr(testWriter{t})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	if got.Machines["box"].Tokens["PROJECTS"] != "/srv/projects" {
		t.Errorf("tokens after init: %v", got.Machines["box"].Tokens)
	}
}
