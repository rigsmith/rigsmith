package climenu

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// labels returns the option labels options() builds for a group, for assertions.
func labels(cmd *cobra.Command) []string {
	var out []string
	for _, o := range options(cmd) {
		out = append(out, o.Key)
	}
	return out
}

func has(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// The menu offers no-arg runnable subcommands and skips arg-required verbs,
// sub-groups, and cobra's generated commands — modelling a `config` group.
func TestOptions_FiltersToNoArgRunnable(t *testing.T) {
	group := &cobra.Command{Use: "config", RunE: func(*cobra.Command, []string) error { return nil }}

	show := &cobra.Command{Use: "show", Short: "Print the whole config", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return nil }}
	get := &cobra.Command{Use: "get", Short: "Print one setting", Args: cobra.MaximumNArgs(1), RunE: func(*cobra.Command, []string) error { return nil }}
	set := &cobra.Command{Use: "set", Short: "Set a value", Args: cobra.ExactArgs(2), RunE: func(*cobra.Command, []string) error { return nil }}
	subgroup := &cobra.Command{Use: "remote"} // no RunE → a sub-group, not a leaf action
	subgroup.AddCommand(&cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error { return nil }})
	hidden := &cobra.Command{Use: "secret", Hidden: true, RunE: func(*cobra.Command, []string) error { return nil }}

	group.AddCommand(show, get, set, subgroup, hidden)
	got := labels(group)

	if !has(got, "show — Print the whole config") {
		t.Errorf("expected show in menu, got %v", got)
	}
	if !has(got, "get — Print one setting") {
		t.Errorf("expected get in menu, got %v", got)
	}
	for _, unwanted := range []string{"set", "remote", "secret"} {
		for _, label := range got {
			if label == unwanted || label == unwanted+" — Set a value" {
				t.Errorf("%q should not be offered, got %v", unwanted, got)
			}
		}
	}
}

// RunMenu with no entries falls back to the parent's help rather than showing
// an empty menu (mirrors Run's empty-options fallback).
func TestRunMenu_EmptyFallsBackToHelp(t *testing.T) {
	var buf bytes.Buffer
	parent := &cobra.Command{Use: "worktree", Short: "Parallel worktrees"}
	parent.SetOut(&buf)
	parent.SetErr(&buf)

	if err := RunMenu(parent, "rig worktree", parent.Short, nil); err != nil {
		t.Fatalf("RunMenu with no entries returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "Parallel worktrees") {
		t.Errorf("expected the group's help, got:\n%s", buf.String())
	}
}

// menuRunnable accepts a NoArgs leaf and rejects a sub-group and an arg-required verb.
func TestMenuRunnable(t *testing.T) {
	leaf := &cobra.Command{Use: "show", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return nil }}
	if !menuRunnable(leaf) {
		t.Error("no-arg leaf should be menu-runnable")
	}
	group := &cobra.Command{Use: "g"}
	if menuRunnable(group) {
		t.Error("a sub-group (no RunE) should not be menu-runnable")
	}
	needsArgs := &cobra.Command{Use: "set", Args: cobra.ExactArgs(2), RunE: func(*cobra.Command, []string) error { return nil }}
	if menuRunnable(needsArgs) {
		t.Error("an arg-required verb should not be menu-runnable")
	}
}
