package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	// Simulate a home configured by an older rig that accepted userinfo.
	mustGitStack(t, root, "config", "--local", "rig.stackHomeURL", remote)
	mustGitStack(t, root, "config", "--local", "rig.stackHomeBranch", "READ_ONLY")
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

func TestStackHomeRejectsStoredCredentials(t *testing.T) {
	root := rmStackspace(t, rmManifest)
	remote := "https://seed-user:seed-secret@example.invalid/seed.git"
	if _, _, err := stackSeedReference(remote); err == nil || strings.Contains(err.Error(), "seed-secret") {
		t.Fatalf("seed reference: %v", err)
	}
	if err := stackConfigureHome(context.Background(), root, remote, "main", ""); err == nil || strings.Contains(err.Error(), "seed-secret") {
		t.Fatalf("credential storage: %v", err)
	}
	if got := mustGitStack(t, root, "config", "--local", "--list"); strings.Contains(got, "seed-secret") {
		t.Fatal("token stored in local configuration")
	}
}

func TestStackHomeGitRedactsCapturedErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Git diagnostic uses a POSIX shell; URL redaction is also tested directly")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\n' 'fatal: cannot access https://seed-user:seed-secret@example.invalid/seed.git' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	_, err := stackHomeGit(context.Background(), dir, "clone", "https://seed-user:seed-secret@example.invalid/seed.git", ".")
	if err == nil || !strings.Contains(err.Error(), "fatal: cannot access") || strings.Contains(err.Error(), "seed-user") || strings.Contains(err.Error(), "seed-secret") {
		t.Fatalf("captured Git error: %v", err)
	}
}

func TestStackHomePublicationDryRun(t *testing.T) {
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
	missing := filepath.Join(t.TempDir(), "does-not-exist.git")
	if err := stackConfigureHome(ctx, root, missing, "main", "unchanged"); err != nil {
		t.Fatal(err)
	}
	stackDryRun(t)
	var out strings.Builder
	if err := stackPublishAfterPull(ctx, repo, m, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "would publish") || !strings.Contains(out.String(), missing) {
		t.Fatalf("preview: %s", out.String())
	}
	if stackHomeConfig(ctx, root, "Base") != "unchanged" {
		t.Fatal("preview changed publication state")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("preview touched remote")
	}
}

func TestStackHomeChecksQuotedMemberNames(t *testing.T) {
	for _, name := range []string{"café", "lib\"one", "\nleading", "trailing\n"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.ContainsAny(name, "\"\n") {
				t.Skip("name is not supported by the Windows filesystem")
			}
			root := rmStackspace(t, rmManifest)
			if err := os.MkdirAll(filepath.Join(root, name), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, name, "file"), []byte("member"), 0644); err != nil {
				t.Fatal(err)
			}
			mustGitStack(t, root, "add", "-A")
			mustGitStack(t, root, "commit", "-qm", "quoted member")
			m := &stackManifest{Repos: map[string]*stackRepo{name: {}}}
			var tree *stackHomeTreeError
			err := stackCheckHome(context.Background(), root, m)
			if !errors.As(err, &tree) || tree.Name != name {
				t.Fatalf("member %q was not detected: %v", name, err)
			}
		})
	}
}

func TestStackHomeRefusesUnrebuildableMembers(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown=%t", unknown), func(t *testing.T) {
			root := rmStackspaceWith(t, rmManifest, !unknown)
			if !unknown {
				if err := os.WriteFile(filepath.Join(root, "pty-core", "src", "local.cs"), []byte("// local only"), 0644); err != nil {
					t.Fatal(err)
				}
				mustGitStack(t, root, "add", "-A")
				mustGitStack(t, root, "commit", "-qm", "unsent member")
			}
			ctx := context.Background()
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
			before := mustGitStack(t, remote, "for-each-ref")
			if err := stackPublishHomeTo(ctx, repo, m, remote, "main", ""); err == nil || !strings.Contains(err.Error(), "commits") {
				t.Fatalf("unrebuildable member accepted: %v", err)
			}
			if got := mustGitStack(t, remote, "for-each-ref"); got != before {
				t.Fatal("refusal moved home")
			}
		})
	}
}

func TestStackHomePublicationIgnoresGlobalHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test hooks use a POSIX shell")
	}
	root := rmStackspace(t, rmManifest)
	ctx := context.Background()
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
	hooks := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooks, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pre-commit", "prepare-commit-msg", "commit-msg", "pre-push"} {
		if err := os.WriteFile(filepath.Join(hooks, name), []byte("#!/bin/sh\nexit 97\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte(fmt.Sprintf("[core]\n hooksPath = %q\n", hooks)), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	if err := stackPublishHomeTo(ctx, repo, m, remote, "main", ""); err != nil {
		t.Fatalf("generated publication ran a global hook: %v", err)
	}
	if got := strings.TrimSpace(mustGitStack(t, remote, "log", "-1", "--format=%s")); got != "stack: update manifest home" {
		t.Fatalf("snapshot message changed: %s", got)
	}
}

func TestStackGuardMemberHome(t *testing.T) {
	for _, tc := range []struct {
		name, home, origin, remote string
		refuse                     bool
	}{
		{"https", "https://github.com/acme/seed.git", "", "github.com/acme/seed", true},
		{"scp", "git@github.com:acme/seed.git", "", "https://github.com/acme/seed", true},
		{"git suffix", "github.com/acme/seed.git", "", "github.com/acme/seed", true},
		{"origin fallback", "", "git@github.com:acme/seed.git", "github.com/acme/seed", true},
		{"different home", "https://github.com/acme/seed.git", "", "github.com/acme/member", false},
		{"configured home wins", "https://github.com/acme/seed.git", "https://github.com/acme/member.git", "github.com/acme/member", false},
		{"unconfigured", "", "", "github.com/acme/member", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mustGitStack(t, root, "init", "-q")
			ctx := context.Background()
			repo, err := gitrepo.Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			if tc.home != "" {
				if err := stackConfigureHome(ctx, root, tc.home, "main", ""); err != nil {
					t.Fatal(err)
				}
			}
			if tc.origin != "" {
				mustGitStack(t, root, "remote", "add", "origin", tc.origin)
			}
			err = stackGuardMemberHome(ctx, repo, tc.remote)
			if (err != nil) != tc.refuse {
				t.Fatalf("guard refusal=%t, want %t: %v", err != nil, tc.refuse, err)
			}
		})
	}
}

func TestStackHomeAutomaticWarnsForEveryUnsentMember(t *testing.T) {
	ctx := context.Background()
	root := rmStackspace(t, rmManifest)
	for _, name := range []string{"pty-core", "term-core"} {
		if err := os.WriteFile(filepath.Join(root, name, "src", "local.cs"), []byte("// local work"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mustGitStack(t, root, "add", "-A")
	mustGitStack(t, root, "commit", "-qm", "unsent work in both members")
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
	if err := stackConfigureHome(ctx, root, remote, "main", ""); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := stackPublishHome(ctx, repo, m, &out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pty-core", "term-core"} {
		if !strings.Contains(out.String(), name+" has commits") {
			t.Fatalf("warning omitted %s: %s", name, out.String())
		}
	}
	if !strings.Contains(out.String(), "warning:") {
		t.Fatalf("missing warning: %s", out.String())
	}
	if got := mustGitStack(t, remote, "for-each-ref"); got == "" {
		t.Fatal("automatic warning blocked publication")
	}
	if err := stackCheckHome(ctx, remote, m); err != nil {
		t.Fatal(err)
	}
}

func TestStackHomeAutomaticDefersDirtyTree(t *testing.T) {
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
	remote := filepath.Join(t.TempDir(), "missing.git")
	if err := stackConfigureHome(ctx, root, remote, "main", "unchanged"); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(root, "untracked-notes")
	if err := os.WriteFile(note, []byte("keep me"), 0644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := stackPublishAfterPull(ctx, repo, m, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "publication deferred") {
		t.Fatalf("missing deferral: %s", out.String())
	}
	if stackHomeConfig(ctx, root, "Base") != "unchanged" {
		t.Fatal("deferral changed home")
	}
	if b, err := os.ReadFile(note); err != nil || string(b) != "keep me" {
		t.Fatal("deferral lost untracked file")
	}
}

func TestStackHomeInitialAdoptionRejectsDifferentRoots(t *testing.T) {
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
	seed := filepath.Join(t.TempDir(), "seed")
	if _, err := stackSeed(ctx, repo, m, seed, false); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(t.TempDir(), "home.git")
	mustGitStack(t, root, "clone", "--bare", seed, remote)
	before := mustGitStack(t, remote, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "new-root-file"), []byte("local root edit"), 0644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, root, "add", "-A")
	mustGitStack(t, root, "commit", "-qm", "root differs")
	for _, force := range []bool{false, true} {
		err := stackPublishHomeToWithPolicy(ctx, repo, m, remote, "main", "", stackHomePublishPolicy{force: force})
		if err == nil || !strings.Contains(err.Error(), "existing seed differs") {
			t.Fatalf("force=%t root mismatch: %v", force, err)
		}
	}
	if got := mustGitStack(t, remote, "rev-parse", "HEAD"); got != before {
		t.Fatal("initial adoption overwrote home")
	}
}
