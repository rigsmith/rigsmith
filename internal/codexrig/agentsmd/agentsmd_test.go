package agentsmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func file(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "AGENTS.md")
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

func TestInstallIsIdempotentAndUpdatesInPlace(t *testing.T) {
	p := file(t, "# my notes\n\nSomething I wrote.\n")
	if act, err := InstallAll(p); err != nil || act != Installed {
		t.Fatalf("act=%v err=%v", act, err)
	}
	first := read(t, p)
	if !strings.Contains(first, "my notes") {
		t.Error("the user's own text was lost")
	}

	if act, err := InstallAll(p); err != nil || act != Unchanged {
		t.Fatalf("a second install reported %v (err %v)", act, err)
	}
	if read(t, p) != first {
		t.Error("a second install rewrote the file")
	}
	if n := strings.Count(read(t, p), Worktree.Begin); n != 1 {
		t.Errorf("the block appears %d times; running twice should update, not append", n)
	}
}

func TestAnEditedBlockIsRewrittenAndNothingElseIs(t *testing.T) {
	p := file(t, "")
	if _, err := InstallAll(p); err != nil {
		t.Fatal(err)
	}
	cur := read(t, p)
	tampered := strings.Replace(cur, "rig worktree new", "SOMEBODY EDITED THIS", 1)
	tampered += "\n## my own section\nkeep me\n"
	if err := os.WriteFile(p, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	if act, err := InstallAll(p); err != nil || act != Updated {
		t.Fatalf("act=%v err=%v", act, err)
	}
	got := read(t, p)
	if strings.Contains(got, "SOMEBODY EDITED THIS") {
		t.Error("an edit inside a managed block survived, which the block's own notice says it will not")
	}
	if !strings.Contains(got, "keep me") {
		t.Error("text outside the block was lost")
	}
}

func TestUninstallLeavesNoGap(t *testing.T) {
	p := file(t, "# top\n")
	if _, err := InstallAll(p); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallAll(p); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if strings.Contains(got, "codexrig:") {
		t.Errorf("a marker survived:\n%s", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("a run of blank lines was left behind:\n%q", got)
	}
	if !strings.HasPrefix(got, "# top") {
		t.Errorf("the user's text moved:\n%q", got)
	}
}

func TestAllPresentIsFalseWhenAnySectionIsMissing(t *testing.T) {
	// So a block added in a later release is treated as something to install
	// rather than as already done.
	p := file(t, "")
	if _, err := Worktree.Install(p); err != nil {
		t.Fatal(err)
	}
	ok, err := AllPresent(p)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("AllPresent said yes with one section missing")
	}
	if _, err := InstallAll(p); err != nil {
		t.Fatal(err)
	}
	if ok, _ := AllPresent(p); !ok {
		t.Error("AllPresent said no after installing everything")
	}
}

func TestAHalfWrittenBlockIsNotPatchedInto(t *testing.T) {
	// A Begin with no End is "not found", so the block is appended rather than
	// spliced into something stranger.
	p := file(t, Worktree.Begin+"\ntruncated, no end marker\n")
	if _, err := Worktree.Install(p); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if !strings.Contains(got, Worktree.End) {
		t.Error("no complete block was written")
	}
	if !strings.Contains(got, "truncated, no end marker") {
		t.Error("the damaged text was silently eaten")
	}
}

func TestTheProseMatchesWhatTheGuardActuallyDoes(t *testing.T) {
	// An instruction file describing a rule that is not enforced is worse than
	// none, because it gets believed. These are the claims the guard has to
	// keep true.
	body := Blocks()
	for _, claim := range []string{
		"main", "master", "trunk", // the base branches it refuses on
		"apply_patch",         // judged whole, all files
		"CODEXRIG_ALLOW_MAIN", // the override
		".codex/allow-main",
		"auth.json", // never travels
	} {
		if !strings.Contains(body, claim) {
			t.Errorf("the guide no longer mentions %q", claim)
		}
	}
	if !strings.Contains(body, "Managed by codexrig") {
		t.Error("a managed block must say so, before somebody edits inside it")
	}
}
