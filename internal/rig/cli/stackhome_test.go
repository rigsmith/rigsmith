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
	t.Run("publication ignores global commit signing", func(t *testing.T) {
		signing := filepath.Join(t.TempDir(), "gitconfig")
		if err := os.WriteFile(signing, []byte("[commit]\n gpgsign = true\n[gpg]\n program = /missing-rig-test-signing-program\n"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", signing)
		if err := stackPublishHomeTo(ctx, repo, m, remote, "main", ""); err != nil {
			t.Fatal(err)
		}
	})
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
	if err := stackPublishAfterPull(ctx, repo, m); err == nil || !strings.Contains(err.Error(), "pull succeeded locally") || !strings.Contains(err.Error(), "retained") {
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

func TestStackHomeDoctor(t *testing.T) {
	ctx := context.Background()
	root := rmStackspace(t, rmManifest)
	m, _, err := loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing.git")
	mustGitStack(t, root, "remote", "add", "origin", missing)
	// An unconfigured stack incurs no remote check, even with a missing origin.
	if err := stackDoctorHome(ctx, root, m); err != nil {
		t.Fatalf("unconfigured doctor: %v", err)
	}
	if err := stackConfigureHome(ctx, root, missing, "main", ""); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	stackReportHomeDoctor(&out, stackDoctorHome(ctx, root, m))
	if !strings.Contains(out.String(), "· seed origin could not be checked:") || strings.Contains(out.String(), "✗") {
		t.Fatalf("unavailable: %s", out.String())
	}
	// An unsafe configured home is a finding even with an unrelated origin.
	bad := filepath.Join(t.TempDir(), "bad.git")
	mustGitStack(t, root, "clone", "--bare", root, bad)
	if err := stackConfigureHome(ctx, root, bad, "main", ""); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stackReportHomeDoctor(&out, stackDoctorHome(ctx, root, m))
	if !strings.Contains(out.String(), "✗ seed origin: seed contains member tree") {
		t.Fatalf("unsafe: %s", out.String())
	}
	// A safe configured seed reports neither a finding nor an access warning.
	repo, err := gitrepo.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(t.TempDir(), "good.git")
	mustGitStack(t, root, "init", "--bare", "--initial-branch=main", good)
	if err := stackPublishHomeTo(ctx, repo, m, good, "main", ""); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, root, "remote", "set-url", "origin", bad)
	if err := stackDoctorHome(ctx, root, m); err != nil {
		t.Fatalf("safe home with unsafe origin: %v", err)
	}
	mustGitStack(t, root, "remote", "remove", "origin")
	if err := stackDoctorHome(ctx, root, m); err != nil {
		t.Fatalf("safe home without origin: %v", err)
	}
	if err := stackConfigureHome(ctx, root, bad, "main", ""); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stackReportHomeDoctor(&out, stackDoctorHome(ctx, root, m))
	if !strings.Contains(out.String(), "✗ seed origin: seed contains member tree") {
		t.Fatalf("unsafe home without origin was not checked: %s", out.String())
	}
	mustGitStack(t, root, "remote", "add", "origin", good)
	out.Reset()
	stackReportHomeDoctor(&out, stackDoctorHome(ctx, root, m))
	if !strings.Contains(out.String(), "✗ seed origin: seed contains member tree") {
		t.Fatalf("unsafe home with safe origin was not checked: %s", out.String())
	}
}

func TestStackHomeRelativeReference(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	local := filepath.Join(root, "work", "rigstack-app")
	if err := os.MkdirAll(local, 0755); err != nil {
		t.Fatal(err)
	}
	remote, ref, err := stackSeedReference("work/rigstack-app")
	resolvedLocal, resolveErr := filepath.EvalSymlinks(local)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if err != nil || remote != resolvedLocal || ref != "" {
		t.Fatalf("local reference became %q@%q: %v", remote, ref, err)
	}
}

func TestStackHomePointerRejectsExtraContent(t *testing.T) {
	for _, s := range []string{"acme/seed@main\nacme/other@main\n", "acme/seed@main extra\n", " acme/seed@main\n", "acme/seed@main\t", "acme/seed@main\x00", "acme/seed@main\n\n"} {
		path := filepath.Join(t.TempDir(), ".rig-stack")
		if err := os.WriteFile(path, []byte(s), 0644); err != nil {
			t.Fatal(err)
		}
		cmd := newStackHomeCmd()
		var out strings.Builder
		cmd.SetOut(&out)
		if err := cmd.RunE(cmd, []string{path}); err == nil || out.Len() != 0 {
			t.Fatalf("accepted pointer %q: %v, output %q", s, err, out.String())
		}
	}
}

func TestStackHomeErrorsRedactCredentials(t *testing.T) {
	const remote = "https://seed-user:seed-secret@example.invalid/seed.git"
	got := stackSafeHomeOutput("fatal: cannot access '" + remote + "': rejected")
	if strings.Contains(got, "seed-user") || strings.Contains(got, "seed-secret") {
		t.Fatalf("git output leaked credentials: %s", got)
	}
	root := rmStackspace(t, rmManifest)
	m, _, err := loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := gitrepo.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := stackConfigureHome(context.Background(), root, remote, "READ_ONLY", ""); err != nil {
		t.Fatal(err)
	}
	err = stackPublishHome(context.Background(), repo, m)
	if err == nil || strings.Contains(err.Error(), "seed-user") || strings.Contains(err.Error(), "seed-secret") {
		t.Fatalf("publication error: %v", err)
	}
}

func TestStackHomeTagOnlyPublicationRefused(t *testing.T) {
	ctx := context.Background()
	root := rmStackspace(t, rmManifest)
	m, _, err := loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := gitrepo.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(t.TempDir(), "tags.git")
	mustGitStack(t, root, "init", "--bare", "--initial-branch=main", remote)
	mustGitStack(t, root, "push", remote, "HEAD:refs/tags/v1")
	before := mustGitStack(t, remote, "show-ref")
	if err := stackPublishHomeTo(ctx, repo, m, remote, "v1", ""); err == nil || !strings.Contains(err.Error(), "is a tag") {
		t.Fatalf("tag accepted: %v", err)
	}
	if got := mustGitStack(t, remote, "show-ref"); got != before {
		t.Fatal("tag publication changed refs")
	}
	if stackHomeConfig(ctx, root, "URL") != "" {
		t.Fatal("tag publication configured a writable home")
	}
}

func TestStackHomeCheckedOutPublicationExplained(t *testing.T) {
	ctx := context.Background()
	root := rmStackspace(t, rmManifest)
	m, _, err := loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := gitrepo.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(t.TempDir(), "seed")
	if _, err := stackSeed(ctx, repo, m, seed, true); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, seed, "config", "receive.denyCurrentBranch", "refuse")
	base := strings.TrimSpace(mustGitStack(t, seed, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(root, "root-note.txt"), []byte("root update\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, root, "add", "-A")
	mustGitStack(t, root, "commit", "-qm", "root update")
	err = stackPublishHomeTo(ctx, repo, m, seed, "main", base)
	if err == nil || !strings.Contains(err.Error(), "bare seed repository") {
		t.Fatalf("checked-out refusal: %v", err)
	}
	if got := strings.TrimSpace(mustGitStack(t, seed, "rev-parse", "HEAD")); got != base {
		t.Fatal("checked-out seed changed")
	}
}
