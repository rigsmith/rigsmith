package cli

import (
	"bytes"
	"context"
	"go.yaml.in/yaml/v3"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStackCISeedValidation(t *testing.T) {
	for _, args := range [][]string{nil, {"--manifest", "x", "--seed", "acme/stack"}} {
		c := newStackCICmd()
		c.SetArgs(args)
		c.SetOut(&bytes.Buffer{})
		c.SetErr(&bytes.Buffer{})
		err := c.Execute()
		code, ok := StackCIExitCode(err)
		if !ok || code != 10 || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("%v: %v", args, err)
		}
	}
	secret := "do-not-print-this-token"
	seed := "https://you:" + secret + "@blocked.invalid/acme/stack.git"
	_, _, err := stackCISeed(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "seed"), seed, "github.com")
	if err == nil || !strings.Contains(err.Error(), "credentials") || strings.Contains(err.Error(), secret) {
		t.Fatal(err)
	}
	seed = "https://blocked.invalid/acme/stack.git"
	_, _, err = stackCISeed(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "seed"), seed, "github.com")
	if err == nil || !strings.Contains(err.Error(), "--allowed-hosts") {
		t.Fatal(err)
	}
	// A local URL rewrite makes the transport failure deterministic and offline.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file:///missing-seed/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://blocked.invalid/")
	_, _, err = stackCISeed(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "seed"), seed, "blocked.invalid")
	if err == nil || !strings.Contains(err.Error(), "fork PRs") || strings.Contains(err.Error(), secret) {
		t.Fatal(err)
	}
}

func TestStackCIActionSeed(t *testing.T) {
	for _, tc := range []struct {
		manifest, seed string
		valid          bool
	}{
		{"", "", false}, {"manifest", "acme/seed@main", false}, {"", "acme/seed@main", true}, {"manifest", "", true},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "rig"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0755); err != nil {
			t.Fatal(err)
		}
		p := exec.Command("bash", "-euo", "pipefail", "-c", stackCIActionScript(t, "Rebuild, wire, build and test"))
		p.Env = append(stackCIActionDefaultEnv(t), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "STACK_FORK_PR=false", "STACK_MANIFEST="+tc.manifest, "STACK_SEED="+tc.seed, "RIG_STACK_CACHE_REPORT="+filepath.Join(dir, "report"), "RIG_STACK_PROVENANCE="+filepath.Join(dir, "provenance"))
		out, err := p.CombinedOutput()
		if (err == nil) != tc.valid {
			t.Fatalf("%+v: %s %v", tc, out, err)
		}
		if tc.valid && tc.seed != "" && !strings.Contains(string(out), "--seed\n"+tc.seed) {
			t.Fatalf("seed not passed: %s", out)
		}
	}
}

func TestStackCIActionSeedCacheKeys(t *testing.T) {
	data, err := os.ReadFile("../../../.github/actions/stack/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Runs struct {
			Steps []struct {
				ID, Name, Uses, If, Run string
				With                    map[string]string
				Env                     map[string]string
			}
		}
	}
	if err = yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	caches := 0
	for _, step := range action.Runs.Steps {
		if strings.HasPrefix(step.Uses, "actions/cache@") {
			caches++
			key := step.With["key"]
			for _, part := range []string{"inputs.seed != '' && format('seed-{0}-{1}-{2}-{3}-{4}'", "steps.cache-source.outputs.seed", "github.run_id", "github.run_attempt", "github.job", "strategy.job-index", "|| hashFiles(inputs.manifest"} {
				if !strings.Contains(key, part) {
					t.Fatalf("%s missing %s: %s", step.Name, part, key)
				}
			}
			if step.With["restore-keys"] == "" || !strings.Contains(step.If, "github.event.pull_request.head.repo.full_name == github.repository") {
				t.Fatalf("missing partial restore or fork guard: %+v", step)
			}
		}
		if step.ID == "cache-source" {
			if step.Env["STACK_SEED"] != "${{ inputs.seed }}" {
				t.Fatal(step.Env)
			}
			// A moving seed reference has a stable namespace; run/attempt in each key
			// above prevents exact hits from blocking subsequent saves.
			var previous string
			for _, seed := range []string{"acme/stack@main", "acme/stack@other"} {
				output := filepath.Join(t.TempDir(), "output")
				p := exec.Command("bash", "-euo", "pipefail", "-c", step.Run)
				p.Env = append(os.Environ(), "STACK_SEED="+seed, "GITHUB_OUTPUT="+output)
				if out, err := p.CombinedOutput(); err != nil {
					t.Fatalf("%v: %s", err, out)
				}
				b, err := os.ReadFile(output)
				if err != nil || !strings.HasPrefix(string(b), "seed=") || string(b) == previous {
					t.Fatalf("namespace: %s %v", b, err)
				}
				previous = string(b)
			}
		}
	}
	if caches != 2 {
		t.Fatalf("found %d cache steps", caches)
	}
}

func TestStackDepsSeed(t *testing.T) {
	work := t.TempDir()
	source := filepath.Join(work, "source")
	mustGitStack(t, work, "init", source)
	mustGitStack(t, source, "config", "user.name", "test")
	mustGitStack(t, source, "config", "user.email", "t@t")
	writeStackManifest(t, source, `{"repos":{"app":{"upstream":"github.com/acme/app","fork":"github.com/you/app"}}}`)
	mustGitStack(t, source, "add", ".")
	mustGitStack(t, source, "commit", "-qm", "seed")
	bare := filepath.Join(work, "seed.git")
	mustGitStack(t, work, "clone", "--bare", source, bare)
	checkout := filepath.Join(work, "checkout")
	mustGitStack(t, work, "init", checkout)
	chdir(t, checkout) // No checkout manifest or HEAD: the seed supplies it.
	c := newStackDepsCmd()
	c.SetArgs([]string{"--seed", bare})
	var out bytes.Buffer
	c.SetOut(&out)
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--seed", bare, "--manifest", "rig.stack.jsonc"},
		{"--seed", "https://blocked.invalid/acme/stack.git"},
		{"--seed", "https://you:secret-token@github.com/acme/stack.git"},
	} {
		c = newStackDepsCmd()
		c.SetArgs(args)
		c.SetOut(&out)
		c.SetErr(&out)
		err := c.Execute()
		if err == nil || strings.Contains(err.Error(), "secret-token") || strings.Contains(out.String(), "secret-token") {
			t.Fatalf("%v: %s", err, out.String())
		}
	}
	remotes, err := stackCIGit(context.Background(), bare, "remote", "get-url", "origin")
	if err != nil || strings.TrimSpace(string(remotes)) != source {
		t.Fatalf("seed mutated: %s %v", remotes, err)
	}
}

func TestStackCIActionSeedDependencies(t *testing.T) {
	script := stackCIActionScript(t, "Report topic dependencies")
	p := exec.Command("bash", "-euo", "pipefail", "-c", "rig() { printf '%s\\n' \"$@\"; };\n"+script)
	p.Env = append(os.Environ(), "STACK_SEED=acme/stack@main", "STACK_MANIFEST=", "STACK_ALLOWED_HOSTS=github.com")
	out, err := p.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--seed\nacme/stack@main\n--allowed-hosts\ngithub.com") || strings.Contains(string(out), "--manifest") {
		t.Fatalf("seed dependency dispatch: %s %v", out, err)
	}
	data, err := os.ReadFile("../../../.github/actions/stack/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "inputs.manifest != ''") {
		t.Fatal("dependency step excludes seed mode")
	}
}

func TestStackCISeedRejectsURLSecrets(t *testing.T) {
	root := rmStackspace(t, rmManifest)
	chdir(t, root)
	for _, base := range []string{"https://github.com/acme/seed.git", "acme/seed", "acme/seed.git", "git@github.com:acme/seed.git", "./seed.git", filepath.Join(root, "seed.git")} {
		for _, suffix := range []string{"?token=query-secret", "#fragment-secret", "?", "#"} {
			t.Run(base+suffix[:1], func(t *testing.T) {
				seed := base + suffix
				for _, mode := range []string{"ci", "deps", "setup", "publish"} {
					t.Run(mode, func(t *testing.T) {
						dir := t.TempDir()
						summary := filepath.Join(dir, "summary")
						provenance := filepath.Join(dir, "provenance")
						t.Setenv("GITHUB_STEP_SUMMARY", summary)
						t.Setenv("RIG_STACK_PROVENANCE", provenance)
						var out bytes.Buffer
						c := newStackCICmd()
						args := []string{"--seed", seed}
						switch mode {
						case "deps":
							c = newStackDepsCmd()
						case "setup":
							c = newStackSetupCmd()
							args = []string{seed}
						case "publish":
							c = newStackSeedCmd()
							args = []string{"--publish", seed}
						}
						c.SetArgs(args)
						c.SetOut(&out)
						c.SetErr(&out)
						err := c.Execute()
						if err == nil || !strings.Contains(err.Error(), "query or fragment") {
							t.Fatalf("missing URL rejection: %v", err)
						}
						if mode == "ci" {
							code, ok := StackCIExitCode(err)
							if !ok || code != 10 {
								t.Fatalf("setup exit: %v", err)
							}
						}
						texts := []string{err.Error(), out.String()}
						for _, path := range []string{summary, provenance} {
							b, e := os.ReadFile(path)
							if e != nil && !os.IsNotExist(e) {
								t.Fatal(e)
							}
							texts = append(texts, string(b))
						}
						for _, text := range texts {
							for _, secret := range []string{"query-secret", "fragment-secret", seed} {
								if strings.Contains(text, secret) {
									t.Fatal("seed URL leaked into diagnostics or artifacts")
								}
							}
						}
						if _, e := os.Stat(provenance); !os.IsNotExist(e) {
							t.Fatalf("invalid seed wrote provenance: %v", e)
						}
					})
				}
			})
		}
	}
}

func TestStackCIReservedSeedMember(t *testing.T) {
	root := t.TempDir()
	writeStackManifest(t, root, `{"repos":{"_seed":{"upstream":"github.com/acme/app","fork":"github.com/you/app"}}}`)
	if _, _, err := loadStackManifest(root); err == nil || !strings.Contains(err.Error(), "reserved for seed provenance") {
		t.Fatalf("reserved member accepted: %v", err)
	}
	// Preserve the core prefix errors and other underscore names.
	if err := stackValidPrefix("../app"); err == nil || !strings.Contains(err.Error(), "single directory") {
		t.Fatalf("prefix validation: %v", err)
	}
	if err := stackValidPrefix("_app"); err != nil {
		t.Fatal(err)
	}
}
