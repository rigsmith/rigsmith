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
	if err := Prepare(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	staged, err := git(t.Context(), root, nil, "show", ":s.jsonl")
	if err != nil || !bytes.Equal(staged, data) {
		t.Fatalf("legacy index retained converted bytes: %q %v", staged, err)
	}
	working, err := os.ReadFile(filepath.Join(root, "s.jsonl"))
	if err != nil || !bytes.Equal(working, data) {
		t.Fatal("migration changed working bytes")
	}
	attrs, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil || !strings.HasPrefix(string(attrs), "*.md diff=markdown\n") {
		t.Fatal("unrelated rules lost")
	}
	if err := Ensure(root); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(root, ".gitattributes"))
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
			err := Prepare(t.Context(), root)
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
	if err := Ensure(root); err == nil {
		t.Fatal("followed symlink")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "untouched" {
		t.Fatal("changed symlink destination")
	}
}
