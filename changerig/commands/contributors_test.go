package commands

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rigsmith/core/changelog"
	"github.com/rigsmith/core/config"
	"github.com/rigsmith/core/ecosystem"
	"github.com/rigsmith/core/planner"
	"github.com/spf13/cobra"
)

// TestAttachContributorsCommitMode drives the real wiring: a commit-mode repo,
// LoadChangesets → planner.Plan → attachContributors, asserting the module picks
// up the commit author (resolved via real `git show`), with no email carried
// through and no GitHub login (the temp repo has no remote).
func TestAttachContributorsCommitMode(t *testing.T) {
	dir := initGoRepo(t, "example.com/a", "v1.0.0")
	// A feature commit after the tag — initGoRepo commits as "Test".
	writeF(t, filepath.Join(dir, "feature.go"), "package main\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "feat: add a feature")

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background()) // cobra sets this during Execute; tests must supply it
	setting := changelog.Setting{Kind: changelog.KindDefault}

	build := func(c config.Contributors) (*Workspace, []*planner.Module) {
		cfg, err := config.Parse([]byte(`{"versioning":{"source":"commits"}}`))
		if err != nil {
			t.Fatal(err)
		}
		cfg.Contributors = c
		ws := &Workspace{Root: dir, ChangesetDir: filepath.Join(dir, ".changeset"), Config: cfg, Registry: ecosystem.Default()}
		pkgs, _, err := ws.Discover(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		sets, _, err := ws.LoadChangesets(context.Background(), pkgs)
		if err != nil {
			t.Fatal(err)
		}
		plan := planner.Plan(sets, pkgs, ws.Config)
		attachContributors(cmd, ws, plan, sets, setting)
		return ws, plan
	}

	t.Run("author attached, no email/login", func(t *testing.T) {
		_, plan := build(config.Contributors{Enabled: true})
		m := findModuleByName(plan, "example.com/a")
		if m == nil {
			t.Fatal("expected example.com/a in the plan")
		}
		if len(m.Contributors) != 1 {
			t.Fatalf("Contributors = %+v, want 1 (Test)", m.Contributors)
		}
		got := m.Contributors[0]
		if got.Name != "Test" {
			t.Errorf("name = %q, want Test", got.Name)
		}
		if got.Email != "" {
			t.Errorf("email must never be carried into the changelog, got %q", got.Email)
		}
		if got.Login != "" {
			t.Errorf("no remote → no login, got %q", got.Login)
		}
		if m.ContributorsSection != config.DefaultContributorsSection {
			t.Errorf("section = %q", m.ContributorsSection)
		}
	})

	t.Run("exclude by name drops the author", func(t *testing.T) {
		_, plan := build(config.Contributors{Enabled: true, Exclude: []string{"Test"}})
		m := findModuleByName(plan, "example.com/a")
		if m != nil && len(m.Contributors) != 0 {
			t.Errorf("excluded author should yield no contributors, got %+v", m.Contributors)
		}
	})
}

func findModuleByName(plan []*planner.Module, name string) *planner.Module {
	for _, m := range plan {
		if m.Name == name {
			return m
		}
	}
	return nil
}
