package cli

import (
	"context"
	"errors"
	"fmt"
	"go.yaml.in/yaml/v3"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
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

func TestStackCIFlow(t *testing.T) {
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
	c := newStackCICmd()
	c.SetArgs([]string{"--manifest", "rig.stack.jsonc", "--member", "app", "--build", "test -f only-in-pr && test -f ../lib/src/lib.txt", "--test", "test -f only-in-pr"})
	if e := c.Execute(); e != nil {
		t.Fatal(e)
	}
	out, _ := os.ReadFile(summary)
	if !strings.Contains(string(out), "test: passed") {
		t.Fatal(string(out))
	}
	status, _ := stackCIGit(context.Background(), checkout, "status", "--porcelain")
	if len(status) != 0 {
		t.Fatalf("checkout modified: %s", status)
	}
	for _, tc := range []struct {
		build, test string
		code        int
	}{{"false", "true", 12}, {"true", "false", 13}} {
		c := newStackCICmd()
		c.SetArgs([]string{"--manifest", "rig.stack.jsonc", "--member", "app", "--build", tc.build, "--test", tc.test})
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
	c.SetArgs([]string{"--manifest", "rig.stack.jsonc", "--member", "app"})
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
	data, err := os.ReadFile("../../../.github/actions/stack/action.yml")
	if err != nil {
		t.Fatal(err)
	}

	var action struct {
		Runs struct {
			Steps []struct {
				ID  string            `yaml:"id"`
				Env map[string]string `yaml:"env"`
				Run string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	// Exercise Windows checkout line endings regardless of the host OS.
	data = []byte(strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n", "\r\n"))
	if err = yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	var script string
	var stepEnv map[string]string
	for _, step := range action.Runs.Steps {
		if step.ID == "run" {
			script = step.Run
			stepEnv = step.Env
		}
	}
	if script == "" {
		t.Fatal("missing run step")
	}
	if stepEnv["STACK_TIP"] != "${{ inputs.upstream-tip }}" {
		t.Fatalf("upstream-tip input mapping: %q", stepEnv["STACK_TIP"])
	}

	if stepEnv["STACK_STRICT"] != "${{ inputs.strict }}" {
		t.Fatalf("strict input mapping: %q", stepEnv["STACK_STRICT"])
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "rig"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, field := range []struct{ env, flag, label string }{{"STACK_TIP", "--upstream-tip", "upstream-tip"}, {"STACK_STRICT", "--strict", "strict"}} {
		for _, value := range []string{"true", "false", "TRUE", "yes", "", " true"} {
			t.Run(fmt.Sprintf("%s=%q", field.label, value), func(t *testing.T) {
				p := exec.Command("bash", "-e", "-c", script)
				p.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "STACK_TIP=false", "STACK_STRICT=false", field.env+"="+value, "STACK_MANIFEST=rig.stack.jsonc", "STACK_MEMBER=app", "STACK_BUILD=true", "STACK_TEST=true")
				output, err := p.CombinedOutput()
				valid := value == "true" || value == "false"
				if valid {
					if err != nil {
						t.Fatalf("%v: %s", err, output)
					}
					if strings.Contains(string(output), field.flag) != (value == "true") {
						t.Fatalf("wrong mode: %s", output)
					}
				} else if err == nil || !strings.Contains(string(output), field.label+" must be exactly true or false") {
					t.Fatalf("want input failure: %v %s", err, output)
				}
			})
		}
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
