package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/codexrig/adapter"
)

func TestHelpAndUnknownCommandsDoNotResolveState(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--version"}, {"inspect", "--help"}, {"sync"}, {"queue"}, {"restore"}} {
		root := newRootCmd("fixture-version", func() (string, error) { t.Fatal("resolved home"); return "", nil }, func(string) string { t.Fatal("read environment"); return "" })
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		err := root.Execute()
		unsupported := args[0] == "sync" || args[0] == "queue" || args[0] == "restore"
		if (err != nil) != unsupported {
			t.Fatal(args, out.String(), err)
		}
	}
}

func TestInspectSourcePrecedenceAndReadOnlyJSON(t *testing.T) {
	home, environment, explicit := t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{filepath.Join(home, ".codex"), environment, explicit} {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("PRIVATE_FIXTURE_CONTENT"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		env  string
		args []string
		want string
	}{
		{"", nil, filepath.Join(home, ".codex")},
		{environment, nil, environment},
		{environment, []string{"--codex-home", explicit}, explicit},
	} {
		root := newRootCmd("dev", func() (string, error) { return home, nil }, func(name string) string {
			if name != "CODEX_HOME" {
				t.Fatal("unexpected environment lookup", name)
			}
			return tc.env
		})
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetArgs(append([]string{"inspect", "--json"}, tc.args...))
		if err := root.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		var report struct {
			Version int
			Mode    string
			Roots   []adapter.Inventory
		}
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatal(out.String(), err)
		}
		if report.Version != 1 || report.Mode != "inventory-only" || len(report.Roots) != 2 || report.Roots[0].Path != tc.want || len(report.Roots[0].Candidates) != 1 || report.Roots[1].Present || strings.Contains(out.String(), "PRIVATE_FIXTURE_CONTENT") {
			t.Fatal(out.String())
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".codex" {
		t.Fatal("created state", entries, err)
	}
}

func TestInspectErrorsDoNotEmitPartialReport(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{{"inspect", "extra"}, {"inspect", "--codex-home", ""}, {"inspect", "--skills-dir", ""}, {"inspect", "--codex-home", "relative"}} {
		root := newRootCmd("dev", func() (string, error) { return home, nil }, func(string) string { return "" })
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(args)
		if err := root.Execute(); err == nil || out.Len() != 0 {
			t.Fatal(args, out.String(), err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	root := newRootCmd("dev", func() (string, error) { return home, nil }, func(string) string { return "" })
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"inspect"})
	if err := root.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSecondRootFailureDoesNotPrintFirstRoot(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	badSkills := filepath.Join(home, "not-a-directory")
	if err := os.WriteFile(badSkills, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd("dev", func() (string, error) { return home, nil }, func(string) string { return "" })
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"inspect", "--json", "--skills-dir", badSkills})
	if err := root.ExecuteContext(t.Context()); err == nil || out.Len() != 0 {
		t.Fatal("partial inventory escaped", out.String(), err)
	}
}
