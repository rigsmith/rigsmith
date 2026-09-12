package backupgit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "core.autocrlf")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	t.Setenv("GIT_CONFIG_KEY_1", "commit.gpgsign")
	t.Setenv("GIT_CONFIG_VALUE_1", "false")
	t.Setenv("GIT_AUTHOR_NAME", "Fixture")
	t.Setenv("GIT_AUTHOR_EMAIL", "fixture@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Fixture")
	t.Setenv("GIT_COMMITTER_EMAIL", "fixture@example.com")
	root := t.TempDir()
	if _, err := git(t.Context(), root, nil, "init", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPrepareRefreshesLegacyIndexWithoutChangingWorkingBytes(t *testing.T) {
	root := setup(t)
	data := []byte("original\r\nbytes\r\n$Id$\r\n")
	if err := os.WriteFile(filepath.Join(root, "s.jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "legacy"}} {
		if _, err := git(t.Context(), root, nil, args...); err != nil {
			t.Fatal(err)
		}
	}
	old, err := git(t.Context(), root, nil, "show", "HEAD:s.jsonl")
	if err != nil || bytes.Equal(old, data) {
		t.Fatalf("fixture was not normalized by Git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.md diff=markdown\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(t.Context(), root, "ClaudeRig"); err != nil {
		t.Fatal(err)
	}
	staged, err := git(t.Context(), root, nil, "show", ":s.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(staged, data) {
		t.Fatalf("legacy index retained converted bytes: %q", staged)
	}
	working, err := os.ReadFile(filepath.Join(root, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(working, data) {
		t.Fatal("migration changed working bytes")
	}
	attrs, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil || !strings.HasPrefix(string(attrs), "*.md diff=markdown\n") {
		t.Fatal("unrelated rules lost")
	}
	if err := Ensure(root, "ClaudeRig"); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(attrs, again) {
		t.Fatal("attribute installation is not idempotent")
	}
}

func TestPrepareRefusesOverridingConversion(t *testing.T) {
	for _, location := range []string{".git/info/attributes", "cli/.gitattributes"} {
		t.Run(location, func(t *testing.T) {
			root := setup(t)
			if err := os.MkdirAll(filepath.Join(root, "cli"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "cli/s.jsonl"), []byte("safe\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, location), []byte("*.jsonl filter=transform\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			err := Prepare(t.Context(), root, "ClaudeRig")
			if err == nil || !strings.Contains(err.Error(), "filter on cli/s.jsonl") {
				t.Fatalf("overriding filter accepted: %v", err)
			}
			staged, err := git(t.Context(), root, nil, "ls-files")
			if err != nil || len(staged) != 0 {
				t.Fatal("refused prepare staged data")
			}
		})
	}
}

func TestEnsureRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "attributes")
	if err := os.WriteFile(target, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".gitattributes")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := Ensure(root, "ClaudeRig"); err == nil {
		t.Fatal("followed symlink")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "untouched" {
		t.Fatal("changed symlink destination")
	}
}

func TestPrepareStagesRequiredAttributesDespiteExcludes(t *testing.T) {
	for _, location := range []string{".gitignore", ".git/info/exclude", "user-excludes"} {
		t.Run(location, func(t *testing.T) {
			root := setup(t)
			p := filepath.Join(root, filepath.FromSlash(location))
			if location == "user-excludes" {
				p = filepath.Join(t.TempDir(), "ignore")
				if _, err := git(t.Context(), root, nil, "config", "core.excludesFile", p); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(p, []byte(".gitattributes\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := Prepare(t.Context(), root, "ClaudeRig"); err != nil {
				t.Fatal(err)
			}
			attrs, err := git(t.Context(), root, nil, "show", ":.gitattributes")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(string(attrs), rule+"\n") {
				t.Fatal("required byte-preservation rules were not staged")
			}
		})
	}
}

// A .gitattributes that stops being synced is deleted from the tree, but the
// index still holds it — and check-attr falls back to the index for a file that
// is gone from disk. Without forgetting the entry, the deleted file goes on
// overriding the backup's own rules and refusing every publish, with nothing
// left on disk to delete.
func TestPrepareForgetsDeletedNestedAttributes(t *testing.T) {
	root := setup(t)
	if err := os.MkdirAll(filepath.Join(root, "cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cli/s.jsonl"), []byte("safe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "cli/.gitattributes")
	if err := os.WriteFile(nested, []byte("* text=auto eol=lf\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// It was synced once, so the index carries it.
	if _, err := git(t.Context(), root, nil, "add", "--force", "--", "cli/.gitattributes"); err != nil {
		t.Fatal(err)
	}
	// The allowlist now excludes it, so sync removed it from the tree.
	if err := os.Remove(nested); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(t.Context(), root, "ClaudeRig"); err != nil {
		t.Fatalf("prepare still refuses after the file is gone: %v", err)
	}
}

// The comment goes into the user's own backup repository, so it has to name the
// rig that wrote it — a codexrig backup explaining itself as clauderig is the
// same mistake as the fallback commit identity that said "clauderig" for both.
// Pinning clauderig's exact wording is also what keeps its compatibility
// baseline green: an existing backup is not rewritten and a new one matches.
func TestTheAttributeCommentNamesTheRigThatWroteIt(t *testing.T) {
	for tool, want := range map[string]string{
		"ClaudeRig": "# ClaudeRig backups must preserve their serialized bytes.",
		"CodexRig":  "# CodexRig backups must preserve their serialized bytes.",
	} {
		dir := t.TempDir()
		if err := Ensure(dir, tool); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir, ".gitattributes"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s wrote:\n%s\nwant a line %q", tool, b, want)
		}
	}
}
