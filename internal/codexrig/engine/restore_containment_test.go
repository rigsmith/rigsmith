package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A restore reads a tree CLONED from a remote, so a symlink in it is not this
// process's own work. Followed, os.ReadFile would restore the bytes it points
// at — /etc/passwd, or the target's own live config — as though they were the
// backup's content.
func TestListStagedFilesNeverOffersASymlink(t *testing.T) {
	stage := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("not the backup's\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "real.toml"), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(stage, "escaping.toml")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	got, err := listStagedFiles(stage)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range got {
		if rel == "escaping.toml" {
			t.Fatalf("a symlink was offered as staged content: %v", got)
		}
	}
	if len(got) != 1 || got[0] != "real.toml" {
		t.Errorf("listStagedFiles = %v, want just real.toml", got)
	}
}

// `restore --dir X` means X. Writing through a link that resolves outside it
// overwrites a file the user never named — for the cli root, their live config.
func TestWriteFileModeRefusesToResolveOutsideTheTarget(t *testing.T) {
	target := t.TempDir()
	outside := filepath.Join(t.TempDir(), "live.toml")
	if err := os.WriteFile(outside, []byte("the machine's own\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(target, "config.toml")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	err := writeFileMode(target, link, []byte("from the backup\n"), 0o600)
	if err == nil {
		t.Fatal("wrote through a link pointing outside the restore target")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Errorf("error does not say why: %v", err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "the machine's own\n" {
		t.Error("the outside file was overwritten anyway")
	}

	// The control: a link INSIDE the target is still written through, which is
	// the behaviour the containment check must not have taken away.
	inner := filepath.Join(target, "real.toml")
	if err := os.WriteFile(inner, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inLink := filepath.Join(target, "alias.toml")
	if err := os.Symlink(inner, inLink); err != nil {
		t.Fatal(err)
	}
	if err := writeFileMode(target, inLink, []byte("new\n"), 0o600); err != nil {
		t.Fatalf("refused a link that stays inside the target: %v", err)
	}
	if b, _ := os.ReadFile(inner); string(b) != "new\n" {
		t.Errorf("did not write through an in-target link: %q", b)
	}
}
