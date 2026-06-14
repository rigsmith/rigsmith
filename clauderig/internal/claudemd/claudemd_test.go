package claudemd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "CLAUDE.md")
	if content != "" {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstall_FreshFile(t *testing.T) {
	p := tmp(t, "")
	act, err := Install(p)
	if err != nil || act != Installed {
		t.Fatalf("act=%v err=%v", act, err)
	}
	got := read(t, p)
	if !strings.Contains(got, Begin) || !strings.Contains(got, End) {
		t.Fatal("markers missing")
	}
	if pres, _ := Present(p); !pres {
		t.Fatal("Present = false after install")
	}
}

func TestInstall_PreservesUserContentAndIsIdempotent(t *testing.T) {
	user := "# My project\n\nSome house rules.\n"
	p := tmp(t, user)
	if _, err := Install(p); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if !strings.HasPrefix(got, user) {
		t.Fatalf("user content not preserved at top:\n%q", got)
	}
	// Second install is a no-op.
	act, _ := Install(p)
	if act != Unchanged {
		t.Fatalf("re-install act = %v, want unchanged", act)
	}
	if read(t, p) != got {
		t.Fatal("idempotent install changed the file")
	}
}

func TestInstall_UpdatesInPlace(t *testing.T) {
	// A stale block (same markers, different body) is rewritten, not duplicated.
	stale := "# Top\n\n" + Begin + "\nOLD TEXT\n" + End + "\n\n## After\nkeep me\n"
	p := tmp(t, stale)
	act, err := Install(p)
	if err != nil || act != Updated {
		t.Fatalf("act=%v err=%v", act, err)
	}
	got := read(t, p)
	if strings.Contains(got, "OLD TEXT") {
		t.Error("stale body survived")
	}
	if strings.Count(got, Begin) != 1 {
		t.Errorf("block duplicated: %d BEGIN markers", strings.Count(got, Begin))
	}
	if !strings.Contains(got, "## After\nkeep me") {
		t.Error("content after the block was lost")
	}
}

func TestUninstall(t *testing.T) {
	p := tmp(t, "# Top\n\nkeep before\n")
	Install(p)
	act, err := Uninstall(p)
	if err != nil || act != Removed {
		t.Fatalf("act=%v err=%v", act, err)
	}
	got := read(t, p)
	if strings.Contains(got, Begin) {
		t.Error("block not removed")
	}
	if !strings.Contains(got, "keep before") {
		t.Error("user content lost on uninstall")
	}
	// Uninstall again is a no-op.
	if act, _ := Uninstall(p); act != Absent {
		t.Errorf("second uninstall act = %v, want absent", act)
	}
}

func TestRigTools_DocumentsEachTool(t *testing.T) {
	// The block carries one subsection per tool, so a session learns all three.
	for _, want := range []string{"### rig", "### changerig", "### shiprig"} {
		if !strings.Contains(RigTools.Block(), want) {
			t.Errorf("rig-tools block missing %q subsection", want)
		}
	}
}

func TestSections_CoexistIndependently(t *testing.T) {
	p := tmp(t, "# My project\n\nHouse rules.\n")
	// Install every managed section the way `guide install` does.
	for _, sec := range Sections {
		if act, err := sec.Install(p); err != nil || act != Installed {
			t.Fatalf("install %s: act=%v err=%v", sec.Begin, act, err)
		}
	}
	got := read(t, p)
	for _, sec := range Sections {
		if !strings.Contains(got, sec.Begin) || !strings.Contains(got, sec.End) {
			t.Fatalf("markers for %s missing after install", sec.Begin)
		}
	}
	if !strings.HasPrefix(got, "# My project") {
		t.Fatal("user content not preserved at top")
	}
	// Re-installing every section is a no-op once both are present.
	for _, sec := range Sections {
		if act, _ := sec.Install(p); act != Unchanged {
			t.Errorf("re-install %s act = %v, want unchanged", sec.Begin, act)
		}
	}
	// Removing one section leaves the other (and the user's content) intact.
	if _, err := Worktree.Uninstall(p); err != nil {
		t.Fatal(err)
	}
	after := read(t, p)
	if strings.Contains(after, Worktree.Begin) {
		t.Error("worktree block survived its uninstall")
	}
	if !strings.Contains(after, RigTools.Begin) {
		t.Error("rig-tools block lost when the worktree block was removed")
	}
	if !strings.Contains(after, "House rules.") {
		t.Error("user content lost on partial uninstall")
	}
}
