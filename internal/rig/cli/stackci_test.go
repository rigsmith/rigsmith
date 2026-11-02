package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

func TestStackCIReplaceCheckout(t *testing.T) {
	work := t.TempDir()
	checkout := filepath.Join(work, "checkout")
	fused := filepath.Join(work, "fused")
	for _, p := range []string{checkout, fused} {
		os.Mkdir(p, 0755)
		mustGitStack(t, p, "init", "-q")
		mustGitStack(t, p, "config", "user.name", "test")
		mustGitStack(t, p, "config", "user.email", "t@t")
	}
	os.WriteFile(filepath.Join(checkout, "only-in-pr"), []byte("PR tree"), 0755)
	os.Mkdir(filepath.Join(checkout, "tests"), 0755)
	os.WriteFile(filepath.Join(checkout, "tests", "kept"), []byte("committed tests"), 0644)
	os.WriteFile(filepath.Join(checkout, "subst"), []byte("$Format:%H$"), 0644)
	os.WriteFile(filepath.Join(checkout, ".gitattributes"), []byte("tests/ export-ignore\nsubst export-subst\n"), 0644)
	mustGitStack(t, checkout, "add", ".")
	mustGitStack(t, checkout, "commit", "-qm", "PR only")
	os.Mkdir(filepath.Join(fused, "app"), 0755)
	os.WriteFile(filepath.Join(fused, "app", "old"), []byte("cursor"), 0644)
	mustGitStack(t, fused, "add", ".")
	mustGitStack(t, fused, "commit", "-qm", "unrelated fused root")
	sha, e := stackCIReplace(context.Background(), checkout, fused, "app")
	if e != nil {
		t.Fatal(e)
	}
	if len(sha) != 40 {
		t.Fatal(sha)
	}
	if _, e = os.Stat(filepath.Join(fused, "app", "old")); !os.IsNotExist(e) {
		t.Fatal("cursor tree survived")
	}
	b, e := os.ReadFile(filepath.Join(fused, "app", "only-in-pr"))
	if e != nil || string(b) != "PR tree" {
		t.Fatalf("%s: %v", b, e)
	}
	for path, want := range map[string]string{"tests/kept": "committed tests", "subst": "$Format:%H$"} {
		data, err := os.ReadFile(filepath.Join(fused, "app", path))
		if err != nil || string(data) != want {
			t.Fatalf("materialized %s: %q, %v", path, data, err)
		}
	}
	os.WriteFile(filepath.Join(checkout, ".gitmodules"), []byte("# submodules"), 0644)
	mustGitStack(t, checkout, "add", ".")
	mustGitStack(t, checkout, "commit", "-qm", "submodules")
	if _, err := stackCIReplace(context.Background(), checkout, fused, "app"); err == nil || !strings.Contains(err.Error(), "submodules") {
		t.Fatalf("want clear submodule failure: %v", err)
	}
	if _, e = os.Stat(filepath.Join(checkout, "only-in-pr")); e != nil {
		t.Fatal(e)
	}
}

func TestStackCIValidation(t *testing.T) {
	c := newStackCICmd()
	c.SetArgs([]string{})
	if e := c.Execute(); e == nil || !strings.Contains(e.Error(), "--manifest is required") {
		t.Fatalf("%v", e)
	}
}

func TestStackCIFlow(t *testing.T) { stackCIFlow(t, false) }

func TestStackCIFlowFailures(t *testing.T) { stackCIFlow(t, true) }

func stackCIFlow(t *testing.T, failures bool) {
	if os.Getenv("RIG_STACK_E2E") == "" {
		t.Skip("set RIG_STACK_E2E=1")
	}
	bin, e := stackJoshProxyBin(stackJoshVersion)
	if e != nil || stackJoshInstalled(bin) != nil {
		t.Fatal("install pinned josh before running E2E")
	}
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/app", "app")
	srv.seed(t, "acme/lib", "lib")
	checkout := filepath.Join(work, "checkout")
	mustGitStack(t, work, "clone", "--depth=1", "--branch=main", "file://"+srv.path("acme/app"), checkout)
	mustGitStack(t, checkout, "config", "user.name", "test")
	mustGitStack(t, checkout, "config", "user.email", "t@t")
	a, _ := stackCIGit(context.Background(), srv.path("acme/app"), "rev-parse", "main")
	b, _ := stackCIGit(context.Background(), srv.path("acme/lib"), "rev-parse", "main")
	os.WriteFile(filepath.Join(checkout, "only-in-pr"), []byte("checkout commit"), 0644)
	writeStackManifest(t, checkout, fmt.Sprintf(`{"repos":{"app":{"upstream":%q,"fork":%q},"lib":{"upstream":%q,"fork":%q}},"lastSync":{"app":%q,"lib":%q}}`, srv.spec("acme/app"), srv.spec("acme/app"), srv.spec("acme/lib"), srv.spec("acme/lib"), strings.TrimSpace(string(a)), strings.TrimSpace(string(b))))
	mustGitStack(t, checkout, "add", ".")
	mustGitStack(t, checkout, "commit", "-qm", "checkout-only PR")
	exe := filepath.Join(work, "rig")
	p := exec.Command("go", "build", "-o", exe, "../../../cmd/rig")
	if out, e := p.CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, out)
	}
	old := stackCIExecutable
	stackCIExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { stackCIExecutable = old })
	chdir(t, checkout)
	summary := filepath.Join(work, "summary")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	t.Setenv("RIG_STACK_CACHE", filepath.Join(work, "cache"))
	report := filepath.Join(work, "cache-report")
	t.Setenv("RIG_STACK_CACHE_REPORT", report)
	t.Setenv("RIG_STACK_PROVENANCE", filepath.Join(work, "provenance.json"))
	var c *cobra.Command
	if !failures {
		c = newStackCICmd()
		c.SetArgs([]string{"--allowed-hosts", srv.addr, "--manifest", "rig.stack.jsonc", "--member", "app", "--build", "test -f only-in-pr && test -f ../lib/src/lib.txt", "--test", "test -f only-in-pr"})
		if e := c.Execute(); e != nil {
			t.Fatal(e)
		}
		out, _ := os.ReadFile(summary)
		if !strings.Contains(string(out), "test: passed") {
			t.Fatal(string(out))
		}
		c = newStackCICmd()
		c.SetArgs([]string{"--allowed-hosts", srv.addr, "--manifest", "rig.stack.jsonc", "--member", "app", "--build", "true", "--test", "true"})
		if err := c.Execute(); err != nil {
			t.Fatal(err)
		}
		cacheReport, err := os.ReadFile(report)
		if err != nil || strings.Count(string(cacheReport), "\tcache\t") != 2 {
			t.Fatalf("repeat run did not reuse both members: %s %v", cacheReport, err)
		}
		provenance, err := os.ReadFile(filepath.Join(work, "provenance.json"))
		if err != nil || !strings.Contains(string(provenance), ":prefix=lib") {
			t.Fatalf("provenance: %s %v", provenance, err)
		}
		if !strings.Contains(string(provenance), `"source": "cache"`) || !strings.Contains(string(provenance), `"filteredCommit":`) {
			t.Fatalf("missing honest cache provenance: %s", provenance)
		}
		c = newStackCICmd()
		c.SetArgs([]string{"--cache-history=false", "--allowed-hosts", srv.addr, "--manifest", "rig.stack.jsonc", "--member", "app", "--build", "true", "--test", "true"})
		if err := c.Execute(); err != nil {
			t.Fatal(err)
		}
		cacheReport, err = os.ReadFile(report)
		if err != nil || strings.Contains(string(cacheReport), "\tcache\t") || strings.Count(string(cacheReport), "\tfetched\t") != 2 {
			t.Fatalf("cache disabled: %s %v", cacheReport, err)
		}
		status, _ := stackCIGit(context.Background(), checkout, "status", "--porcelain")
		if len(status) != 0 {
			t.Fatalf("checkout modified: %s", status)
		}
		return
	}
	for _, tc := range []struct {
		build, test string
		code        int
	}{{"false", "true", 12}, {"true", "false", 13}} {
		c := newStackCICmd()
		c.SetArgs([]string{"--allowed-hosts", srv.addr, "--manifest", "rig.stack.jsonc", "--member", "app", "--build", tc.build, "--test", tc.test})
		e := c.Execute()
		var ce *stackCIError
		if !errors.As(e, &ce) || ce.ExitCode() != tc.code {
			t.Fatalf("want %d: %v", tc.code, e)
		}
	}
	wrapper := filepath.Join(work, "rig-wrapper")
	os.WriteFile(wrapper, []byte("#!/bin/bash\nif [[ $2 == wire ]]; then exit 7; fi\nexec "+exe+" \"$@\"\n"), 0755)
	stackCIExecutable = func() (string, error) { return wrapper, nil }
	c = newStackCICmd()
	c.SetArgs([]string{"--allowed-hosts", srv.addr, "--manifest", "rig.stack.jsonc", "--member", "app"})
	e = c.Execute()
	var ce *stackCIError
	if !errors.As(e, &ce) || ce.ExitCode() != 11 {
		t.Fatalf("want wire failure: %v", e)
	}

}

func TestStackCIGitSeparatesStderr(t *testing.T) {
	dir := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(dir, "git")
	if err = os.WriteFile(wrapper, []byte("#!/bin/sh\necho warning >&2\nexec '"+git+"' \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	data, err := stackCIGit(context.Background(), dir, "--version")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "warning") || !strings.HasPrefix(string(data), "git version") {
		t.Fatalf("stderr contaminated stdout: %q", data)
	}
}

func TestStackCISetupExecutableFailure(t *testing.T) {
	summary := filepath.Join(t.TempDir(), "summary")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	old := stackCIExecutable
	stackCIExecutable = func() (string, error) { return "", errors.New("executable unavailable") }
	t.Cleanup(func() { stackCIExecutable = old })
	c := newStackCICmd()
	c.SetArgs([]string{"--manifest", "rig.stack.jsonc"})
	err := c.Execute()
	var stepErr *stackCIError
	if !errors.As(err, &stepErr) || stepErr.ExitCode() != 10 {
		t.Fatalf("want setup exit 10: %v", err)
	}
	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "setup: failed: executable unavailable") {
		t.Fatalf("missing failure summary: %s", data)
	}
}

func TestStackCIActionBoolean(t *testing.T) {
	script := stackCIActionScript(t, "Rebuild, wire, build and test")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rig"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"STACK_TIP", "STACK_CACHE_HISTORY", "STACK_CACHE_PACKAGES", "STACK_DEPENDENCIES", "STACK_STRICT"} {
		for _, value := range []string{"true", "false", "TRUE", "yes", "", " true"} {
			t.Run(fmt.Sprintf("%s=%q", input, value), func(t *testing.T) {
				p := exec.Command("bash", "-e", "-c", script)
				p.Env = append(stackCIActionDefaultEnv(t), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "STACK_FORK_PR=false", input+"="+value, "STACK_MANIFEST=rig.stack.jsonc", "STACK_MEMBER=app", "STACK_BUILD=true", "STACK_TEST=true")
				output, err := p.CombinedOutput()
				valid := value == "true" || value == "false"
				if valid {
					if err != nil {
						t.Fatalf("%v: %s", err, output)
					}
					if input == "STACK_STRICT" && strings.Contains(string(output), "--strict") != (value == "true") {
						t.Fatalf("wrong strict mode: %s", output)
					}
					if input == "STACK_TIP" && strings.Contains(string(output), "--upstream-tip") != (value == "true") {
						t.Fatalf("wrong mode: %s", output)
					}
				} else if err == nil || !strings.Contains(string(output), map[string]string{"STACK_TIP": "upstream-tip", "STACK_CACHE_HISTORY": "cache-history", "STACK_CACHE_PACKAGES": "cache-packages", "STACK_DEPENDENCIES": "dependencies", "STACK_STRICT": "strict"}[input]+" must be exactly true or false") {
					t.Fatalf("want input failure: %v %s", err, output)
				}
			})
		}
	}
}

// Derive defaults so a new action input does not break unrelated script fixtures.
func stackCIActionDefaultEnv(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("../../../.github/actions/stack/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Inputs map[string]struct{ Default string }
		Runs   struct {
			Steps []struct {
				ID  string
				Env map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	reference := regexp.MustCompile(`^\$\{\{ inputs\.([A-Za-z_][A-Za-z0-9_-]*) \}\}$`)
	for _, step := range action.Runs.Steps {
		if step.ID != "run" {
			continue
		}
		env := os.Environ()
		keys := make([]string, 0, len(step.Env))
		for key := range step.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			match := reference.FindStringSubmatch(step.Env[key])
			if len(match) == 0 {
				continue
			}
			input := match[1]
			definition, ok := action.Inputs[input]
			if !ok {
				t.Fatalf("step env %s names undefined input %s", key, input)
			}
			env = append(env, key+"="+definition.Default)
		}
		return env
	}
	t.Fatal("missing action run step")
	return nil
}

func stackCIActionScript(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../../../.github/actions/stack/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Runs struct {
			Steps []struct {
				Name string
				Env  map[string]string
				Run  string
			}
		}
	}
	// Exercise Windows checkout line endings regardless of the host OS.
	data = []byte(strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n", "\r\n"))
	if err = yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	for _, step := range action.Runs.Steps {
		if step.Name == name {
			if name == "Rebuild, wire, build and test" {
				for key, input := range map[string]string{"STACK_TIP": "upstream-tip", "STACK_CACHE_HISTORY": "cache-history", "STACK_CACHE_PACKAGES": "cache-packages", "STACK_DEPENDENCIES": "dependencies", "STACK_STRICT": "strict"} {
					if step.Env[key] != "${{ inputs."+input+" }}" {
						t.Fatalf("input %s mapping: %q", input, step.Env[key])
					}
				}
			}
			return step.Run
		}
	}
	t.Fatalf("missing action script %s", name)
	return ""
}
func TestStackCIActionCacheReport(t *testing.T) {
	script := stackCIActionScript(t, "Report consumed member cache hits")
	for _, rows := range []string{"", "app\tcache\tsha\nlib\tfetched\tsha\n"} {
		t.Run(fmt.Sprint(len(rows)), func(t *testing.T) {
			dir := t.TempDir()
			summary := filepath.Join(dir, "summary")
			if rows != "" {
				os.WriteFile(filepath.Join(dir, "stack-cache-report"), []byte(rows), 0600)
			}
			p := exec.Command("bash", "-euo", "pipefail", "-c", script)
			p.Env = append(os.Environ(), "RUNNER_TEMP="+dir, "GITHUB_STEP_SUMMARY="+summary, "STACK_FORK_PR=false")
			if out, err := p.CombinedOutput(); err != nil {
				t.Fatalf("report: %v %s", err, out)
			}
			data, err := os.ReadFile(summary)
			want := "0/0 consumed hits (0%)"
			if rows != "" {
				want = "1/2 consumed hits (50%)"
			}
			if err != nil || !strings.Contains(string(data), want) {
				t.Fatalf("report %s: %v", data, err)
			}
		})
	}
}

func TestStackCIExitCode(t *testing.T) {
	for _, code := range []int{10, 11, 12, 13} {
		got, ok := StackCIExitCode(fmt.Errorf("wrapped: %w", &stackCIError{"phase", code, errors.New("failed")}))
		if !ok || got != code {
			t.Fatalf("want %d: %d %v", code, got, ok)
		}
	}
	for _, err := range []error{nil, errors.New("failure"), &exec.ExitError{}, &exitError{code: 7}} {
		if code, ok := StackCIExitCode(err); ok {
			t.Fatalf("unexpected propagation %T: %d", err, code)
		}
	}
}

func TestStackCIMaterializeSymlinks(t *testing.T) {
	for _, target := range []string{"/outside", `\outside`, "../../outside", "kept"} {
		t.Run(target, func(t *testing.T) {
			checkout := t.TempDir()
			dest := t.TempDir()
			mustGitStack(t, checkout, "init", "-q")
			mustGitStack(t, checkout, "config", "user.name", "test")
			mustGitStack(t, checkout, "config", "user.email", "t@t")
			if err := os.WriteFile(filepath.Join(checkout, "kept"), []byte("source"), 0644); err != nil {
				t.Fatal(err)
			}

			mustGitStack(t, checkout, "add", ".")
			stackCITestSymlinkEntry(t, checkout, "link", target)

			mustGitStack(t, checkout, "commit", "-qm", "symlink tree")
			err := stackCIMaterialize(context.Background(), checkout, dest)
			if target != "kept" {
				if err == nil || !strings.Contains(err.Error(), "escapes prefix") {
					t.Fatalf("want escape refusal: %v", err)
				}
				return
			}

			if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
				if !strings.Contains(err.Error(), `cannot create member symlink "link"`) {
					t.Fatalf("missing file diagnostic: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			link, err := os.Readlink(filepath.Join(dest, "link"))
			if err != nil || link != target {
				t.Fatalf("link %q: %v", link, err)
			}
		})
	}
}

func TestStackCIMaterializePathEscape(t *testing.T) {
	checkout := t.TempDir()
	dest := t.TempDir()
	outside := t.TempDir()
	mustGitStack(t, checkout, "init", "-q")
	mustGitStack(t, checkout, "config", "user.name", "test")
	mustGitStack(t, checkout, "config", "user.email", "t@t")
	if err := os.Mkdir(filepath.Join(checkout, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "src", "file"), []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, checkout, "add", ".")
	mustGitStack(t, checkout, "commit", "-qm", "nested tree")
	if err := os.Symlink(outside, filepath.Join(dest, "src")); err != nil {
		t.Fatal(err)
	}
	err := stackCIMaterialize(context.Background(), checkout, dest)
	if err == nil || !strings.Contains(err.Error(), "path escapes prefix") {
		t.Fatalf("want path escape refusal: %v", err)
	}
	if _, err = os.Stat(filepath.Join(outside, "file")); !os.IsNotExist(err) {
		t.Fatalf("outside file was touched: %v", err)
	}
	// Keep '..' unresolved until insideRoot walks existing symlinks.
	for _, suffix := range []string{"src/../outside", "src" + string(filepath.Separator) + ".." + string(filepath.Separator) + "outside"} {
		if insideRoot(dest, dest+string(filepath.Separator)+suffix) {
			t.Fatalf("insideRoot accepted traversal through an escaping symlink: %q", suffix)
		}
	}

}

// Create a committed link independently of Windows checkout/symlink privileges.
func stackCITestSymlinkEntry(t *testing.T, checkout, path, target string) {
	t.Helper()
	blob := filepath.Join(t.TempDir(), "link-blob")
	if err := os.WriteFile(blob, []byte(target), 0644); err != nil {
		t.Fatal(err)
	}
	sha, err := stackCIGit(context.Background(), checkout, "hash-object", "-w", blob)
	if err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, checkout, "update-index", "--add", "--cacheinfo", "120000,"+strings.TrimSpace(string(sha))+","+path)
}

func TestStackCIMaterializeSymlinkPermission(t *testing.T) {
	checkout := t.TempDir()
	dest := t.TempDir()
	mustGitStack(t, checkout, "init", "-q")
	mustGitStack(t, checkout, "config", "user.name", "test")
	mustGitStack(t, checkout, "config", "user.email", "t@t")
	stackCITestSymlinkEntry(t, checkout, "link", "kept")
	mustGitStack(t, checkout, "commit", "-qm", "committed symlink")
	old := stackCISymlink
	stackCISymlink = func(string, string) error { return os.ErrPermission }
	t.Cleanup(func() { stackCISymlink = old })
	err := stackCIMaterialize(context.Background(), checkout, dest)
	if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), `cannot create member symlink "link" -> "kept"`) {
		t.Fatalf("want named permission error: %v", err)
	}
	if !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("missing runner guidance: %v", err)
	}
}

func TestStackCIActionForkCachePolicy(t *testing.T) {
	data, err := os.ReadFile("../../../.github/actions/stack/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Inputs map[string]struct{ Default string }
		Runs   struct {
			Steps []struct {
				Name string
				If   string
				Uses string
				Env  map[string]string
			}
		}
	}
	if err = yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	guard := "(!github.event.pull_request || github.event.pull_request.head.repo.full_name == github.repository)"
	fork := "${{ github.event.pull_request != null && github.event.pull_request.head.repo.full_name != github.repository }}"
	seen := map[string]bool{}
	for _, step := range action.Runs.Steps {
		switch step.Name {
		case "Restore josh binaries and object history", "Restore package downloads":
			input := "cache-history"
			if step.Name == "Restore package downloads" {
				input = "cache-packages"
			}
			want := "${{ inputs." + input + " == 'true' && " + guard + " }}"
			if step.If != want {
				t.Fatalf("%s must gate both restore and post-save on trusted repo: %q", step.Name, step.If)
			}
			if !strings.HasPrefix(step.Uses, "actions/cache@") {
				t.Fatalf("unexpected cache implementation: %s", step.Uses)
			}
			if action.Inputs[input].Default != "true" {
				t.Fatalf("%s should default true for non-fork jobs", input)
			}
			seen[step.Name] = true
		case "Rebuild, wire, build and test":
			want := "${{ inputs.cache-history == 'true' && " + guard + " && format('{0}/stack-history', runner.temp) || '' }}"
			if step.Env["RIG_STACK_CACHE"] != want {
				t.Fatalf("fork jobs must receive an empty bundle cache: %q", step.Env["RIG_STACK_CACHE"])
			}
			if step.Env["STACK_FORK_PR"] != fork {
				t.Fatalf("missing fork classification: %q", step.Env["STACK_FORK_PR"])
			}
			seen[step.Name] = true
		case "Report consumed member cache hits":
			if step.Env["STACK_FORK_PR"] != fork {
				t.Fatalf("report must classify fork jobs: %q", step.Env["STACK_FORK_PR"])
			}
			seen[step.Name] = true
		}
	}
	if len(seen) != 4 {
		t.Fatalf("missing cache policy steps: %v", seen)
	}
}

func TestStackCIActionForkCacheExecution(t *testing.T) {
	for _, fork := range []string{"true", "false"} {
		t.Run(fork, func(t *testing.T) {
			dir := t.TempDir()
			cache := filepath.Join(dir, "history")
			if err := os.WriteFile(filepath.Join(dir, "rig"), []byte("#!/bin/sh\nprintf 'CACHE=%s\n' \"${RIG_STACK_CACHE-unset}\"\nprintf '%s\n' \"$@\"\n"), 0755); err != nil {
				t.Fatal(err)
			}
			p := exec.Command("bash", "-euo", "pipefail", "-c", stackCIActionScript(t, "Rebuild, wire, build and test"))
			p.Env = append(stackCIActionDefaultEnv(t), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "STACK_FORK_PR="+fork, "STACK_MANIFEST=rig.stack.jsonc", "STACK_MEMBER=app", "STACK_BUILD=true", "STACK_TEST=true", "RIG_STACK_CACHE="+cache, "RIG_STACK_CACHE_REPORT="+filepath.Join(dir, "report"), "RIG_STACK_PROVENANCE="+filepath.Join(dir, "provenance"))
			out, err := p.CombinedOutput()
			if err != nil {
				t.Fatalf("run script: %v %s", err, out)
			}
			wantCache, wantFlag := cache, "--cache-history=true"
			if fork == "true" {
				wantCache, wantFlag = "unset", "--cache-history=false"
			}
			if !strings.Contains(string(out), "CACHE="+wantCache) || !strings.Contains(string(out), wantFlag) {
				t.Fatalf("fork=%s inherited cache access: %s", fork, out)
			}
			summary := filepath.Join(dir, "summary")
			if err = os.WriteFile(filepath.Join(dir, "stack-cache-report"), []byte("app\tcache\tsha\n"), 0600); err != nil {
				t.Fatal(err)
			}
			p = exec.Command("bash", "-euo", "pipefail", "-c", stackCIActionScript(t, "Report consumed member cache hits"))
			p.Env = append(os.Environ(), "RUNNER_TEMP="+dir, "GITHUB_STEP_SUMMARY="+summary, "STACK_FORK_PR="+fork)
			if out, err = p.CombinedOutput(); err != nil {
				t.Fatalf("summary script: %v %s", err, out)
			}
			text, err := os.ReadFile(summary)
			if err != nil {
				t.Fatal(err)
			}
			if fork == "true" {
				if !strings.Contains(string(text), "skipped because this is a fork PR") || strings.Contains(string(text), "consumed hits") {
					t.Fatalf("misleading fork cache summary: %s", text)
				}
			} else if !strings.Contains(string(text), "1/1 consumed hits") {
				t.Fatalf("non-fork report changed: %s", text)
			}
		})
	}
}

func TestStackCIActionGuardProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rig"), []byte("#!/bin/sh\nprintf '%s\\000' \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	project := "tests/Acme App Tests/Acme App Tests.csproj"
	p := exec.Command("bash", "-euo", "pipefail", "-c", stackCIActionScript(t, "Rebuild, wire, build and test"))
	p.Env = append(stackCIActionDefaultEnv(t), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "STACK_FORK_PR=false", "STACK_GUARD_PROJECT="+project, "RIG_STACK_CACHE_REPORT="+filepath.Join(dir, "report"), "RIG_STACK_PROVENANCE="+filepath.Join(dir, "provenance"))
	output, err := p.CombinedOutput()
	if err != nil {
		t.Fatalf("action run failed: %v %s", err, output)
	}
	args := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	found := 0
	for i, arg := range args {
		if arg == "--guard-project" {
			found++
			if i+1 >= len(args) || args[i+1] != project {
				t.Fatalf("guard path split or changed: %q", args)
			}
		}
	}
	if found != 1 {
		t.Fatalf("want one --guard-project argument: %q", args)
	}
}
