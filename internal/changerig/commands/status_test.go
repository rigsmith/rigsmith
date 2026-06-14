package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/spf13/cobra"
)

// panelOutput renders printEmptyStatusPanel for a workspace with the given
// source and two packages, under a root command named tool, and returns the
// plain text.
func panelOutput(t *testing.T, tool string, source config.VersioningSource) string {
	t.Helper()
	cfg := config.Default()
	cfg.Versioning.Source = source
	ws := &Workspace{Root: "/repo", ChangesetDir: "/repo/.changeset", Config: cfg}
	pkgs := []plugin.Package{
		{Name: "pkg-b", Version: "2.1.0"},
		{Name: "pkg-a", Version: "1.0.0"},
	}
	ecoOf := map[string]string{"pkg-a": "node", "pkg-b": "node"}

	var buf bytes.Buffer
	cmd := &cobra.Command{Use: tool}
	cmd.SetOut(&buf)
	printEmptyStatusPanel(cmd, ws, pkgs, ecoOf)
	return buf.String()
}

// The panel shows the source, both packages at their current versions (sorted),
// the nothing-pending line, and a changeset-mode next step naming the binary.
func TestEmptyStatusPanelChangesetMode(t *testing.T) {
	out := panelOutput(t, "shiprig", config.SourceChangesets)

	for _, want := range []string{
		"Source:", "changesets",
		"Packages (2)",
		"pkg-a", "1.0.0", "pkg-b", "2.1.0",
		"node",
		"Nothing to release yet.",
		"shiprig add", // source-aware next step uses the root command name
	} {
		if !strings.Contains(out, want) {
			t.Errorf("panel output missing %q:\n%s", want, out)
		}
	}

	// pkg-a sorts before pkg-b.
	if strings.Index(out, "pkg-a") > strings.Index(out, "pkg-b") {
		t.Errorf("packages not sorted by name:\n%s", out)
	}
}

// In commits mode the panel's next step points at a conventional commit, not
// `add`, and reports the commit source.
func TestEmptyStatusPanelCommitsMode(t *testing.T) {
	out := panelOutput(t, "changerig", config.SourceCommits)

	if !strings.Contains(out, "commits") {
		t.Errorf("panel should report the commits source:\n%s", out)
	}
	if !strings.Contains(out, "conventional commit") {
		t.Errorf("commits-mode next step should mention a conventional commit:\n%s", out)
	}
	if strings.Contains(out, "changerig add") {
		t.Errorf("commits mode should not tell the user to run `add`:\n%s", out)
	}
}
