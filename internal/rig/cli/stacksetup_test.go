package cli

import (
	"bytes"
	"context"
	"fmt"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// setup does four things in order, and the first two are refusals. Both come
// before the engine, which is the point: acquiring it can be a multi-minute
// build, and neither of these states is going to need it.
func TestStackSetupRefusesBeforeItInstallsAnything(t *testing.T) {
	ctx := context.Background()

	t.Run("no manifest", func(t *testing.T) {
		inTempStackspace(t, "")
		err := runVerb(ctx, newStackSetupCmd())
		if err == nil || !strings.Contains(err.Error(), "rig stack init") {
			t.Fatalf("err = %v, want a pointer at init", err)
		}
	})

	t.Run("the untouched scaffold", func(t *testing.T) {
		// An empty manifest loads fine — it is what init writes. Setting up
		// nothing used to install the engine, generate a memberless README and
		// report success, which is a worse answer than the one this state has.
		inTempStackspace(t, "{\n  \"repos\": {}\n}\n")
		err := runVerb(ctx, newStackSetupCmd())
		if err == nil || !strings.Contains(err.Error(), "no repos yet") {
			t.Fatalf("err = %v, want the add-a-repo error", err)
		}
	})
}

// The menu runs a command that was never attached to a parent, so a step looked
// up by walking Parent() is a nil dereference — in the one place a first-timer
// is most likely to arrive from.
func TestStackSetupStepsResolveWithoutAParent(t *testing.T) {
	orphan := &cobra.Command{Use: "setup"}
	orphan.SetContext(context.Background())
	var out bytes.Buffer
	orphan.SetOut(&out)
	orphan.SetErr(&out)

	// status outside a stackspace is an error, not a panic: reaching its RunE
	// at all is what this asserts.
	if err := runStackSubcommand(orphan, "status"); err == nil && out.Len() == 0 {
		t.Fatal("the step did not run")
	}
	if err := runStackSubcommand(orphan, "nope"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("err = %v, want the internal missing-verb error", err)
	}
	// Every step setup names has to be one of them.
	for _, name := range []string{"init", "wire", "status"} {
		if _, ok := stackSteps[name]; !ok {
			t.Errorf("setup runs %q, which is not a known step", name)
		}
	}
}

// Stop at the engine boundary: home adoption must be settled before any engine
// install or import, neither of which these local configuration tests require.
type stackSetupProbe struct{ reached bool }

func (p *stackSetupProbe) Write(b []byte) (int, error) {
	if strings.Contains(string(b), "· fusion engine") {
		p.reached = true
		panic(p)
	}
	return len(b), nil
}
func probeStackSetup(t *testing.T, args ...string) {
	t.Helper()
	p := &stackSetupProbe{}
	func() {
		defer func() {
			if r := recover(); r != nil && r != p {
				panic(r)
			}
		}()
		cmd := newStackSetupCmd()
		cmd.SetContext(context.Background())
		cmd.SetOut(p)
		if err := cmd.RunE(cmd, args); err != nil {
			t.Errorf("setup before engine: %v", err)
		}
	}()
	if !p.reached {
		t.Error("setup did not reach the engine step")
	}
}

func TestStackSetupNoArgumentDoesNotAdoptOrigin(t *testing.T) {
	for _, fused := range []bool{true, false} {
		t.Run(fmt.Sprintf("fused=%t", fused), func(t *testing.T) {
			root := rmStackspace(t, rmManifest)
			if !fused {
				m, _, err := loadStackManifest(root)
				if err != nil {
					t.Fatal(err)
				}
				repo, err := gitrepo.Open(context.Background(), root)
				if err != nil {
					t.Fatal(err)
				}
				seed := filepath.Join(t.TempDir(), "seed")
				if _, err := stackSeed(context.Background(), repo, m, seed, true); err != nil {
					t.Fatal(err)
				}
				root = seed
				chdir(t, root)
			}
			mustGitStack(t, root, "remote", "add", "origin", filepath.Join(t.TempDir(), "origin.git"))
			before := mustGitStack(t, root, "config", "--local", "--list")
			probeStackSetup(t)
			after := mustGitStack(t, root, "config", "--local", "--list")
			if before != after {
				t.Errorf("no-argument setup changed local configuration:\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}

func TestStackSetupConfiguredNoArgumentKeepsHome(t *testing.T) {
	root := rmStackspace(t, rmManifest)
	mustGitStack(t, root, "remote", "add", "origin", "/unused/seed.git")
	if err := stackConfigureHome(context.Background(), root, "/unused/seed.git", "main", "remembered-tip"); err != nil {
		t.Fatal(err)
	}
	before := mustGitStack(t, root, "config", "--local", "--list")
	probeStackSetup(t)
	if got := mustGitStack(t, root, "config", "--local", "--list"); got != before {
		t.Fatalf("configured setup changed git configuration")
	}
}

func TestStackSetupStoresRawOriginWithCredentialRewrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("clone wrapper uses a POSIX shell")
	}
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
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := t.TempDir()
	script := "#!/bin/sh\n\"$RIG_TEST_REAL_GIT\" \"$@\"\nstatus=$?\nif [ \"$status\" -eq 0 ] && [ \"$3\" = clone ]; then\n \"$RIG_TEST_REAL_GIT\" -C \"$2\" config --local remote.origin.url \"$RIG_TEST_SEED_URL\"\nfi\nexit \"$status\"\n"
	if err := os.WriteFile(filepath.Join(wrapper, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte("[url \"https://x-access-token:test-token@github.com/\"]\n insteadOf = https://github.com/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RIG_TEST_REAL_GIT", realGit)
	t.Setenv("RIG_TEST_SEED_URL", "https://github.com/acme/seed.git")
	dest := t.TempDir()
	chdir(t, dest)
	probeStackSetup(t, seed)
	if stackHomeConfig(ctx, dest, "URL") != "https://github.com/acme/seed.git" {
		t.Fatal("setup did not save raw origin")
	}
	if strings.Contains(mustGitStack(t, dest, "config", "--local", "--list"), "test-token") {
		t.Fatal("token stored in clone configuration")
	}
	if !strings.Contains(mustGitStack(t, dest, "remote", "get-url", "origin"), "test-token") {
		t.Fatal("test did not exercise credential rewrite")
	}
}

func TestStackSetupSeparatesPinFromBranchName(t *testing.T) {
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
	mustGitStack(t, seed, "tag", "v1")
	mustGitStack(t, seed, "branch", "READ_ONLY")
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte(fmt.Sprintf("[url %q]\n insteadOf = https://github.com/acme/seed.git\n", seed)), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	for _, ref := range []string{"v1", "READ_ONLY"} {
		t.Run(ref, func(t *testing.T) {
			dest := t.TempDir()
			chdir(t, dest)
			probeStackSetup(t, "acme/seed@"+ref)
			if got := stackHomeConfig(ctx, dest, "Pinned"); (got == "true") != (ref == "v1") {
				t.Fatalf("pinned=%s for %s", got, ref)
			}
			wantBranch := ref
			if ref == "v1" {
				wantBranch = ""
			}
			if got := stackHomeConfig(ctx, dest, "Branch"); got != wantBranch {
				t.Fatalf("branch=%q, want %q", got, wantBranch)
			}
		})
	}
}

func TestStackSetupLocalSeedUnderHashDirectory(t *testing.T) {
	source := rmStackspace(t, rmManifest)
	parent := filepath.Join(t.TempDir(), "workspace#local")
	if err := os.Mkdir(parent, 0755); err != nil {
		t.Fatal(err)
	}
	var err error
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "stack")
	mustGitStack(t, parent, "clone", source, root)
	mustGitStack(t, root, "config", "user.name", "test")
	mustGitStack(t, root, "config", "user.email", "t@t")
	seed := filepath.Join(parent, "seed.git")
	mustGitStack(t, parent, "init", "--bare", "--initial-branch=main", seed)
	chdir(t, root)
	publish := newStackSeedCmd()
	publish.SetArgs([]string{"--publish", "../seed.git", "--force"})
	if err := publish.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := stackHomeConfig(context.Background(), root, "URL"); got != seed {
		t.Fatalf("stored home: %q", got)
	}
	// Retrying uses the stored absolute remote, rather than the original input.
	retry := newStackSeedCmd()
	retry.SetArgs([]string{"--force"})
	if err := retry.Execute(); err != nil {
		t.Fatal(err)
	}
	chdir(t, parent)
	remote, _, err := stackSeedReference("./seed.git")
	if err != nil || remote != seed {
		t.Fatalf("relative reference: %q %v", remote, err)
	}
	if _, _, err := stackCISeed(context.Background(), parent, filepath.Join(t.TempDir(), "ci-seed"), "./seed.git", "github.com"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(parent, "checkout")
	if err := os.Mkdir(dest, 0755); err != nil {
		t.Fatal(err)
	}
	chdir(t, dest)
	probeStackSetup(t, "../seed.git")
	if got := stackHomeConfig(context.Background(), dest, "URL"); got != seed {
		t.Fatalf("adopted home: %q", got)
	}
}

func TestStackSetupCleansCloneAfterConfigurationFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git configuration fault injection uses a POSIX wrapper")
	}
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
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := t.TempDir()
	// Inject a configuration failure after Git has successfully cloned.
	script := "#!/bin/sh\nif [ \"$3\" = config ] && [ \"$5\" = rig.stackHomeURL ]; then echo 'injected configuration failure' >&2; exit 1; fi\nexec \"$RIG_TEST_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrapper, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIG_TEST_REAL_GIT", realGit)
	t.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))
	dest := t.TempDir()
	chdir(t, dest)
	cmd := newStackSetupCmd()
	cmd.SetArgs([]string{seed})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "injected configuration failure") {
		t.Fatalf("configuration error: %v", err)
	}
	entries, err := os.ReadDir(dest)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed setup left clone contents: %v %v", entries, err)
	}
	// An existing destination is never cleaned.
	if err := os.WriteFile(filepath.Join(dest, "keep"), []byte("mine"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd = newStackSetupCmd()
	cmd.SetArgs([]string{seed})
	if err := cmd.Execute(); err == nil {
		t.Fatal("nonempty destination accepted")
	}
	if data, err := os.ReadFile(filepath.Join(dest, "keep")); err != nil || string(data) != "mine" {
		t.Fatal("existing destination changed")
	}
}
