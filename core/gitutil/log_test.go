package gitutil

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLogSinceReturnsCommitsWithFiles(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	tag := "v1.0.0"
	git(t, dir, "tag", tag)

	// A feature commit touching one package, with a breaking-change footer.
	writeFile(t, filepath.Join(dir, "packages", "pkg-a", "main.go"), "package a\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "feat(core): add thing\n\nMore detail.\n\nBREAKING CHANGE: drops X")

	// A second commit touching two packages.
	writeFile(t, filepath.Join(dir, "packages", "pkg-a", "extra.go"), "package a\n")
	writeFile(t, filepath.Join(dir, "packages", "pkg-b", "main.go"), "package b\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "fix: cross-cutting")

	commits, err := LogSince(ctx, dir, tag)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2: %+v", len(commits), commits)
	}

	// Newest first.
	if commits[0].Subject != "fix: cross-cutting" {
		t.Errorf("commits[0].Subject = %q, want fix: cross-cutting", commits[0].Subject)
	}
	wantFiles := map[string]bool{
		filepath.Join(dir, "packages", "pkg-a", "extra.go"): true,
		filepath.Join(dir, "packages", "pkg-b", "main.go"):  true,
	}
	if len(commits[0].Files) != 2 {
		t.Fatalf("commits[0].Files = %v, want 2 files", commits[0].Files)
	}
	for _, f := range commits[0].Files {
		if !wantFiles[f] {
			t.Errorf("unexpected file %q", f)
		}
		if !filepath.IsAbs(f) {
			t.Errorf("path %q is not absolute", f)
		}
	}

	feat := commits[1]
	if feat.Subject != "feat(core): add thing" {
		t.Errorf("feat.Subject = %q", feat.Subject)
	}
	if want := "More detail.\n\nBREAKING CHANGE: drops X"; feat.Body != want {
		t.Errorf("feat.Body = %q, want %q", feat.Body, want)
	}
}

func TestLogSinceEmptyRefReadsWholeHistory(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	writeFile(t, filepath.Join(dir, "x.txt"), "x\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "second")

	commits, err := LogSince(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	// initial + second.
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2 (whole history)", len(commits))
	}
}

func TestLogSinceInvalidRef(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	if _, err := LogSince(ctx, dir, "no-such-ref"); err == nil {
		t.Error("invalid ref should error")
	}
	if _, err := LogSince(ctx, t.TempDir(), ""); err == nil {
		t.Error("non-repo should error")
	}
}

func TestParseGitHubSlug(t *testing.T) {
	cases := []struct{ url, want string }{
		{"git@github.com:JohnCampionJr/rigsmith.git", "JohnCampionJr/rigsmith"},
		{"https://github.com/unjs/changelogen.git", "unjs/changelogen"},
		{"https://github.com/unjs/changelogen", "unjs/changelogen"},
		{"ssh://git@github.com/acme/widgets.git", "acme/widgets"},
		{"git@gitlab.com:acme/widgets.git", ""}, // not github
		{"https://example.com/a/b", ""},
		{"github.com/only-owner", ""},
	}
	for _, c := range cases {
		if got := parseGitHubSlug(c.url); got != c.want {
			t.Errorf("parseGitHubSlug(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
