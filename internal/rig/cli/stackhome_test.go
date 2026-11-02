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
		{"C:/work/rigstack-app.git", "C:/work/rigstack-app.git", ""},
		{`C:\work\rigstack-app.git`, `C:\work\rigstack-app.git`, ""},
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
	if err != nil || !filepath.IsAbs(remote) || ref != "" {
		t.Fatalf("local reference became %q@%q: %v", remote, ref, err)
	}
	expected, err := os.Stat(local)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.Stat(remote)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(expected, actual) {
		t.Fatalf("local reference %q does not identify %q", remote, local)
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
		{"GitHub case", "https://GITHUB.COM/acme/seed.git", "", "git@github.com:Acme/Seed.git", true},
		{"local case", "/tmp/CaseSeed", "", "/tmp/caseseed", false},
		{"GitLab host case", "https://GitLab.com/acme/seed.git", "", "gitlab.com/acme/seed", true},
		{"GitLab path case", "https://GitLab.com/Acme/Seed.git", "", "gitlab.com/acme/seed", false},
		{"self-hosted host case", "https://Forge.Acme.test/acme/seed.git", "", "forge.acme.test/acme/seed", true},
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

func TestStackHomePublishesIgnoredCommittedRoots(t *testing.T) {
	ctx := context.Background()
	root := rmStackspace(t, rmManifest)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.keep\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.keep"), []byte("committed despite ignore\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, root, "add", "--force", ".gitignore", "root.keep")
	mustGitStack(t, root, "commit", "-qm", "tracked ignored root")
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
	if got := mustGitStack(t, remote, "show", "HEAD:root.keep"); got != "committed despite ignore\n" {
		t.Fatalf("ignored committed root omitted: %q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "new.keep"), []byte("new tracked root\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, root, "add", "--force", "new.keep")
	mustGitStack(t, root, "commit", "-qm", "another tracked ignored root")
	if err := stackPublishHome(ctx, repo, m); err != nil {
		t.Fatal(err)
	}
	if got := mustGitStack(t, remote, "show", "HEAD:new.keep"); got != "new tracked root\n" {
		t.Fatalf("new ignored root omitted: %q", got)
	}
}

func TestStackHomeDoctorUsesConfiguredRevision(t *testing.T) {
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
	safe := strings.TrimSpace(mustGitStack(t, remote, "rev-parse", "main"))
	unsafe := strings.TrimSpace(mustGitStack(t, root, "rev-parse", "HEAD"))
	mustGitStack(t, root, "push", remote, "HEAD:refs/heads/unsafe")
	if err := stackConfigureHome(ctx, root, remote, "unsafe", ""); err != nil {
		t.Fatal(err)
	}
	var tree *stackHomeTreeError
	if err := stackDoctorHome(ctx, root, m); !errors.As(err, &tree) {
		t.Fatalf("configured unsafe branch missed: %v", err)
	}
	mustGitStack(t, remote, "symbolic-ref", "HEAD", "refs/heads/unsafe")
	for _, tc := range []struct {
		branch, base string
		bad          bool
	}{
		{"main", "", false}, {"READ_ONLY", safe, false}, {"READ_ONLY", unsafe, true},
	} {
		branch := tc.branch
		pinned := branch == "READ_ONLY"
		if pinned {
			branch = ""
		}
		if err := stackConfigureHomeState(ctx, root, remote, branch, tc.base, pinned); err != nil {
			t.Fatal(err)
		}
		err := stackDoctorHome(ctx, root, m)
		tree = nil
		if tc.bad {
			if !errors.As(err, &tree) {
				t.Fatalf("unsafe pinned revision missed: %v", err)
			}
		} else if err != nil {
			t.Fatalf("safe configured revision checked wrong branch: %v", err)
		}
	}
}

func TestStackGuardMemberHomeURLRewrites(t *testing.T) {
	for _, rule := range []string{"insteadOf", "pushInsteadOf"} {
		t.Run(rule, func(t *testing.T) {
			root := t.TempDir()
			mustGitStack(t, root, "init", "-q")
			ctx := context.Background()
			repo, err := gitrepo.Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(t.TempDir(), "home.git")
			if err := stackConfigureHome(ctx, root, home, "main", ""); err != nil {
				t.Fatal(err)
			}
			member := "https://example.invalid/member.git"
			mustGitStack(t, root, "config", "--local", "url."+home+"."+rule, member)
			before := mustGitStack(t, root, "config", "--local", "--list")
			if err := stackGuardMemberHome(ctx, repo, member); err == nil || !strings.Contains(err.Error(), "member destination is the manifest home") {
				t.Fatalf("rewrite to home accepted: %v", err)
			}
			if after := mustGitStack(t, root, "config", "--local", "--list"); after != before {
				t.Fatal("URL resolution saved a temporary remote")
			}
			if err := stackGuardMemberHome(ctx, repo, "https://example.invalid/other.git"); err != nil {
				t.Fatalf("unrelated member blocked: %v", err)
			}
		})
	}
}

func TestStackEffectivePushURLMatchesGit(t *testing.T) {
	root := t.TempDir()
	mustGitStack(t, root, "init", "-q")
	remote := "https://example.invalid/member/lib.git"
	mustGitStack(t, root, "remote", "add", "rewrite-check", remote)
	for _, rule := range []struct{ key, value string }{
		{"url.https://example.invalid/broad/.insteadOf", "https://example.invalid/"},
		{"url.https://example.invalid/specific/.insteadOf", "https://example.invalid/member/"},
		{"url." + filepath.ToSlash(filepath.Join(t.TempDir(), "seed")) + "/.pushInsteadOf", "https://example.invalid/"},
	} {
		mustGitStack(t, root, "config", "--local", "--add", rule.key, rule.value)
		want := strings.TrimSpace(mustGitStack(t, root, "remote", "get-url", "--push", "rewrite-check"))
		got, err := stackEffectivePushURL(context.Background(), root, remote)
		if err != nil || got != want {
			t.Fatalf("effective destination %q, want Git's %q: %v", got, want, err)
		}
	}
}

func TestStackHomePinnedPublicationPolicy(t *testing.T) {
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
	if err := stackConfigureHomeState(ctx, root, remote, "", "pinned-commit", true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dirty"), []byte("local"), 0644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := stackPublishAfterPull(ctx, repo, m, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "manifest home is pinned — publication skipped\n" {
		t.Fatalf("pinned automatic output: %q", out.String())
	}
	cmd := newStackSeedCmd()
	cmd.SetContext(ctx)
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("explicit pinned retry: %v", err)
	}
	if err := cmd.Flags().Set("publish", remote); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("explicit pinned publish: %v", err)
	}
	if _, err := os.Stat(remote); !os.IsNotExist(err) {
		t.Fatal("pinned publication touched remote")
	}
}

func TestStackHomeReadOnlyBranchIsWritable(t *testing.T) {
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
	mustGitStack(t, root, "init", "--bare", "--initial-branch=READ_ONLY", remote)
	if err := stackPublishHomeTo(ctx, repo, m, remote, "READ_ONLY", ""); err != nil {
		t.Fatal(err)
	}
	if stackHomeConfig(ctx, root, "Pinned") != "false" {
		t.Fatal("branch was marked pinned")
	}
	if err := os.WriteFile(filepath.Join(root, "root-note"), []byte("new root"), 0644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, root, "add", "-A")
	mustGitStack(t, root, "commit", "-qm", "root")
	if err := stackPublishHome(ctx, repo, m); err != nil {
		t.Fatal(err)
	}
	if got := mustGitStack(t, remote, "show", "refs/heads/READ_ONLY:root-note"); got != "new root" {
		t.Fatal("READ_ONLY branch did not update")
	}
	if err := stackDoctorHome(ctx, root, m); err != nil {
		t.Fatalf("doctor misread branch sentinel: %v", err)
	}
}

func TestStackHomeAdoptionProtectsOnlyMatchingOrigin(t *testing.T) {
	for _, matching := range []bool{false, true} {
		t.Run(fmt.Sprint(matching), func(t *testing.T) {
			ctx := context.Background()
			root := rmStackspace(t, rmManifest)
			remote := filepath.Join(t.TempDir(), "home.git")
			mustGitStack(t, root, "init", "--bare", "--initial-branch=main", remote)
			origin := remote
			if !matching {
				origin = filepath.Join(t.TempDir(), "other.git")
			}
			mustGitStack(t, root, "remote", "add", "origin", origin)
			mustGitStack(t, root, "remote", "set-url", "--push", "origin", "custom://preserve")
			cmd := newStackSeedCmd()
			cmd.SetContext(ctx)
			if err := cmd.Flags().Set("publish", remote); err != nil {
				t.Fatal(err)
			}
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatal(err)
			}
			got := strings.TrimSpace(mustGitStack(t, root, "config", "--local", "--get", "remote.origin.pushurl"))
			if !matching {
				if got != "custom://preserve" {
					t.Fatal("unrelated origin push URL changed")
				}
				return
			}
			if got != "disabled://fused-stackspace" {
				t.Fatal("adoption left seed origin pushable")
			}
			before := mustGitStack(t, remote, "for-each-ref")
			if _, err := stackHomeGit(ctx, root, "push", "origin", "HEAD:refs/heads/leak"); err == nil {
				t.Fatal("plain push reached home")
			}
			if after := mustGitStack(t, remote, "for-each-ref"); after != before {
				t.Fatal("plain push moved home")
			}
		})
	}
}

func TestStackHomeRejectsMalformedPointers(t *testing.T) {
	for _, s := range []string{"acme/seed@bad..ref", "acme/seed@bad@{ref", "acme/seed@-bad", "acme/seed@trailing/", "https://", "https://[bad/seed", "https://example.invalid:bad/seed", "https://example.invalid/%zz"} {
		path := filepath.Join(t.TempDir(), ".rig-stack")
		if err := os.WriteFile(path, []byte(s+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		cmd := newStackHomeCmd()
		var out strings.Builder
		cmd.SetOut(&out)
		if err := cmd.RunE(cmd, []string{path}); err == nil || out.Len() != 0 {
			t.Fatalf("invalid pointer accepted: %q, %v", s, err)
		}
	}
}

func TestStackHomeRejectsURLSecrets(t *testing.T) {
	root := rmStackspace(t, rmManifest)
	ctx := context.Background()
	repo, err := gitrepo.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"?token=query-secret", "#fragment-secret", "?", "#"} {
		remote := "https://github.com/acme/seed.git" + suffix
		check := func(err error) {
			t.Helper()
			if err == nil || !strings.Contains(err.Error(), "query or fragment") || strings.Contains(err.Error(), remote) || strings.Contains(err.Error(), "query-secret") || strings.Contains(err.Error(), "fragment-secret") {
				t.Fatalf("unsafe rejection: %v", err)
			}
		}
		_, _, err := stackSeedReference(remote)
		check(err)
		check(stackConfigureHome(ctx, root, remote, "main", ""))
		// Legacy configured homes must fail before the retry hint echoes the URL.
		mustGitStack(t, root, "config", "rig.stackHomeURL", remote)
		check(stackPublishConfiguredHome(ctx, repo, nil, stackHomePublishPolicy{}))
		mustGitStack(t, root, "config", "--unset", "rig.stackHomeURL")
	}
}
