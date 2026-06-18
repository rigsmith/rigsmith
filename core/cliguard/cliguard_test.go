package cliguard

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// rulesAt returns the set of rule ids reported for the given command path.
func rulesAt(vs []Violation, path string) map[string]bool {
	out := map[string]bool{}
	for _, v := range vs {
		if v.Path == path {
			out[v.Rule] = true
		}
	}
	return out
}

func TestCheck_FlagConventions(t *testing.T) {
	root := &cobra.Command{Use: "tool", Run: func(*cobra.Command, []string) {}}

	// A canonical flag with the right shorthand is clean.
	ok := &cobra.Command{Use: "ok", Run: func(*cobra.Command, []string) {}}
	ok.Flags().BoolP("dry-run", "n", false, "")
	ok.Flags().BoolP("yes", "y", false, "")

	// dry-run with the wrong letter → reserved-shorthand.
	wrongLetter := &cobra.Command{Use: "wrongletter", Run: func(*cobra.Command, []string) {}}
	wrongLetter.Flags().BoolP("dry-run", "d", false, "")

	// -n used for something other than dry-run → reserved-letter.
	stolenLetter := &cobra.Command{Use: "stolenletter", Run: func(*cobra.Command, []string) {}}
	stolenLetter.Flags().BoolP("nuke", "n", false, "")

	// a boolean --list flag → list-flag.
	listFlag := &cobra.Command{Use: "listflag", Run: func(*cobra.Command, []string) {}}
	listFlag.Flags().Bool("list", false, "")

	root.AddCommand(ok, wrongLetter, stolenLetter, listFlag)
	vs := Check(root)

	if r := rulesAt(vs, "tool ok"); len(r) != 0 {
		t.Errorf("ok command should be clean, got %v", r)
	}
	if r := rulesAt(vs, "tool wrongletter"); !r["reserved-shorthand"] {
		t.Errorf("wrong shorthand not flagged: %v", r)
	}
	if r := rulesAt(vs, "tool stolenletter"); !r["reserved-letter"] {
		t.Errorf("stolen reserved letter not flagged: %v", r)
	}
	if r := rulesAt(vs, "tool listflag"); !r["list-flag"] {
		t.Errorf("--list flag not flagged: %v", r)
	}
}

func TestCheck_DoctorNeedsFix(t *testing.T) {
	root := &cobra.Command{Use: "tool", Run: func(*cobra.Command, []string) {}}
	bare := &cobra.Command{Use: "doctor", Run: func(*cobra.Command, []string) {}}
	fixed := &cobra.Command{Use: "doctor", Run: func(*cobra.Command, []string) {}}
	fixed.Flags().Bool("fix", false, "")

	root.AddCommand(bare)
	if r := rulesAt(Check(root), "tool doctor"); !r["doctor-fix"] {
		t.Errorf("doctor without --fix not flagged: %v", r)
	}

	root2 := &cobra.Command{Use: "tool", Run: func(*cobra.Command, []string) {}}
	root2.AddCommand(fixed)
	if r := rulesAt(Check(root2), "tool doctor"); r["doctor-fix"] {
		t.Errorf("doctor with --fix should be clean: %v", r)
	}
}

func TestCheck_GroupMenu(t *testing.T) {
	root := &cobra.Command{Use: "tool", Run: func(*cobra.Command, []string) {}}

	// A pure group (subcommands, no RunE/Run) should open a menu → flagged.
	group := &cobra.Command{Use: "group"}
	group.AddCommand(&cobra.Command{Use: "child", Run: func(*cobra.Command, []string) {}})

	// A group that DOES run interactively (like account/mcp) → clean.
	interactive := &cobra.Command{Use: "interactive", RunE: func(*cobra.Command, []string) error { return nil }}
	interactive.AddCommand(&cobra.Command{Use: "child", Run: func(*cobra.Command, []string) {}})

	root.AddCommand(group, interactive)
	vs := Check(root)
	if r := rulesAt(vs, "tool group"); !r["group-menu"] {
		t.Errorf("bare group not flagged: %v", r)
	}
	if r := rulesAt(vs, "tool interactive"); r["group-menu"] {
		t.Errorf("interactive group should be clean: %v", r)
	}
}

func TestReport_GroupsByRule(t *testing.T) {
	out := Report([]Violation{
		{Tool: "tool", Path: "tool b", Rule: "group-menu", Detail: "x"},
		{Tool: "tool", Path: "tool a", Rule: "group-menu", Detail: "y"},
	})
	if !strings.Contains(out, "group-menu (2)") {
		t.Errorf("report missing grouped header:\n%s", out)
	}
	// Paths within a rule are sorted.
	if strings.Index(out, "tool a") > strings.Index(out, "tool b") {
		t.Errorf("paths not sorted within rule:\n%s", out)
	}
}
