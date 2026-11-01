package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

func TestStackHomeReference(t *testing.T) {
	for _, tc := range []struct{ in, remote, ref string }{
		{"acme/rigstack-app@main", "https://github.com/acme/rigstack-app.git", "main"},
		{"acme/rigstack-app@topic/fix", "https://github.com/acme/rigstack-app.git", "topic/fix"},
		{"git@example.com:acme/seed.git", "git@example.com:acme/seed.git", ""},
		{"/tmp/seed.git", "/tmp/seed.git", ""},
	} {
		remote, ref, err := stackSeedReference(tc.in)
		if err != nil || remote != tc.remote || ref != tc.ref {
			t.Fatalf("%s: %s %s %v", tc.in, remote, ref, err)
		}
	}
}

func TestStackHomePublication(t *testing.T) {
	ctx := context.Background()
	root := rmStackspace(t, rmManifest)
	repo, err := gitrepo.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(t.TempDir(), "home.git")
	mustGitStack(t, root, "init", "--bare", "--initial-branch=main", remote)
	if err := stackPublishHomeTo(ctx, repo, m, remote, "main", ""); err != nil {
		t.Fatal(err)
	}
	base := stackHomeConfig(ctx, root, "Base")
	if base == "" {
		t.Fatal("missing seed base")
	}
	fused := mustGitStack(t, root, "rev-parse", "HEAD")
	c := mustGitStack(t, remote, "rev-list", "--all")
	if strings.Contains(c, fused) {
		t.Fatal("fused commit reached seed")
	}
	if err := stackCheckHome(ctx, remote, m); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "rig.stack.jsonc")
	b, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, append(b, []byte("\n// cursor record\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, root, "add", "-A")
	mustGitStack(t, root, "commit", "-qm", "cursor")
	if err := stackPublishHome(ctx, repo, m); err != nil {
		t.Fatal(err)
	}
	if got := mustGitStack(t, remote, "rev-list", "--count", "HEAD"); strings.TrimSpace(got) != "2" {
		t.Fatalf("want one update commit, got %s", got)
	}
	tip := mustGitStack(t, remote, "rev-parse", "HEAD")
	if err := stackPublishHomeTo(ctx, repo, m, remote, "main", base); err == nil || !strings.Contains(err.Error(), "seed moved") {
		t.Fatalf("stale seed: %v", err)
	}
	if got := mustGitStack(t, remote, "rev-parse", "HEAD"); got != tip {
		t.Fatal("remote changed on refusal")
	}
	if got := mustGitStack(t, root, "log", "-1", "--format=%s"); strings.TrimSpace(got) != "cursor" {
		t.Fatal("local work lost")
	}
	// A rejected push must preserve local work and the published tip.
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, append(b, []byte("\n// another cursor\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, root, "add", "-A")
	mustGitStack(t, root, "commit", "-qm", "retained")
	if err := stackPublishHome(ctx, repo, m); err == nil || !strings.Contains(err.Error(), "retained") {
		t.Fatalf("push refusal: %v", err)
	}
	if got := mustGitStack(t, remote, "rev-parse", "HEAD"); got != tip {
		t.Fatal("rejected push moved seed")
	}

	bad := filepath.Join(t.TempDir(), "bad.git")
	mustGitStack(t, root, "clone", "--bare", root, bad)
	if err := stackPublishHomeTo(ctx, repo, m, bad, "", ""); err == nil || !strings.Contains(err.Error(), "member tree") {
		t.Fatalf("fused home accepted: %v", err)
	}
}

func TestStackHomePointer(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".rig-stack")
	if err := os.WriteFile(path, []byte("acme/rigstack-app@main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := newStackHomeCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{path}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "acme/rigstack-app@main\n" {
		t.Fatalf("pointer: %q", out.String())
	}
}
