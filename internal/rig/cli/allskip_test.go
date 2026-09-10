package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// plainCmd is a command wired to a buffer, as the plain (non-TTY) `--all` path
// sees it.
func plainCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	return cmd, &buf
}

// A package that doesn't define the verb is skipped and the fan-out carries on
// — the whole point: one workspace package without a `typecheck` script used to
// abort every package after it.
func TestRunAcrossPlain_SkipsPackagesWithoutTheVerb(t *testing.T) {
	tasks := []allTask{
		{name: "@acme/auth", eco: "node"},
		{name: "@acme/docs", eco: "node", skip: `no "typecheck" script`},
		{name: "@acme/web", eco: "node"},
	}
	cmd, buf := plainCmd(t)
	var ran []string
	err := runAcrossPlain(cmd, tasks, "typecheck", func(task allTask) error {
		ran = append(ran, task.name)
		return nil
	})
	if err != nil {
		t.Fatalf("a skipped package must not fail the run: %v", err)
	}
	if strings.Join(ran, ",") != "@acme/auth,@acme/web" {
		t.Fatalf("ran = %v, want the two packages that define typecheck", ran)
	}
	out := buf.String()
	// Skipped is reported, never silently dropped.
	if !strings.Contains(out, "@acme/docs") || !strings.Contains(out, `no "typecheck" script`) {
		t.Errorf("output should name the skipped package and why:\n%s", out)
	}
	if !strings.Contains(out, "✓ 2 ok") || !strings.Contains(out, "– 1 skipped") {
		t.Errorf("summary should count 2 ok / 1 skipped:\n%s", out)
	}
}

// The distinction that matters: a script that RAN and failed is still a
// failure, and still fails the whole run — unlike a package that never defined
// the verb, which is the absence of work.
//
// It fails the run without ENDING it: the packages after the failure still run,
// so one CI log names every broken package instead of only the first.
func TestRunAcrossPlain_FailingPackageStillFails(t *testing.T) {
	tasks := []allTask{
		{name: "@acme/docs", eco: "node", skip: `no "test" script`},
		{name: "@acme/core", eco: "node", argv: []string{"sh", "-c", "exit 1"}, dir: t.TempDir()},
		{name: "@acme/web", eco: "node", argv: []string{"sh", "-c", "exit 0"}, dir: t.TempDir()},
	}
	cmd, buf := plainCmd(t)
	err := runAcrossPlain(cmd, tasks, "test", func(task allTask) error {
		return runCommand(cmd, task.dir, task.argv)
	})
	if err == nil {
		t.Fatalf("a failing script must fail the run (output: %q)", buf.String())
	}
	if !strings.Contains(err.Error(), "@acme/core") {
		t.Errorf("err = %v, want it to name the failing package", err)
	}
	// The package after the failure still ran.
	out := buf.String()
	if !strings.Contains(out, "@acme/web") {
		t.Errorf("a failure must not end the run — @acme/web should still have run:\n%s", out)
	}
	if !strings.Contains(out, "✓ 1 ok") || !strings.Contains(out, "✗ 1 failed") || !strings.Contains(out, "– 1 skipped") {
		t.Errorf("summary should count 1 ok / 1 failed / 1 skipped:\n%s", out)
	}
}

// Several broken packages are all named, in one run — the reason the fan-out
// carries on rather than stopping at the first.
func TestRunAcrossPlain_ReportsEveryFailure(t *testing.T) {
	tasks := []allTask{
		{name: "@acme/core", eco: "node", argv: []string{"sh", "-c", "exit 1"}, dir: t.TempDir()},
		{name: "@acme/web", eco: "node", argv: []string{"sh", "-c", "exit 0"}, dir: t.TempDir()},
		{name: "@acme/api", eco: "node", argv: []string{"sh", "-c", "exit 2"}, dir: t.TempDir()},
	}
	cmd, buf := plainCmd(t)
	err := runAcrossPlain(cmd, tasks, "build", func(task allTask) error {
		return runCommand(cmd, task.dir, task.argv)
	})
	if err == nil {
		t.Fatalf("failures must fail the run (output: %q)", buf.String())
	}
	for _, want := range []string{"2 packages", "@acme/core", "@acme/api"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}
	if out := buf.String(); !strings.Contains(out, "✓ 1 ok") || !strings.Contains(out, "✗ 2 failed") {
		t.Errorf("summary should count 1 ok / 2 failed:\n%s", out)
	}
}

// A `--all` run where no package defines the verb has nothing to do; say so
// rather than reporting a silent success.
func TestRunAllTasks_EverythingSkipped(t *testing.T) {
	cmd, _ := plainCmd(t)
	tasks := []allTask{
		{name: "@acme/docs", eco: "node", skip: `no "lint" script`},
		{name: "@acme/web", eco: "node", skip: `no "lint" script`},
	}
	err := runAllTasks(cmd, tasks, "lint")
	if err == nil {
		t.Fatal("want an error when nothing could run")
	}
	if !strings.Contains(err.Error(), `no workspace package defines "lint"`) ||
		!strings.Contains(err.Error(), `no "lint" script`) {
		t.Errorf("err = %v, want the verb and the reason", err)
	}
}

// End to end over a real pnpm workspace: the packages that declare `typecheck`
// run, the one that doesn't is skipped, and the run succeeds.
func TestRunAcross_NodeWorkspaceSkipsMissingScript(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"name":"root","version":"0.0.0","private":true}`)
	write("pnpm-workspace.yaml", "packages:\n  - 'packages/*'\n")
	write("pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	write("packages/auth/package.json", `{"name":"@acme/auth","version":"1.0.0","scripts":{"typecheck":"tsc --noEmit"}}`)
	write("packages/docs/package.json", `{"name":"@acme/docs","version":"1.0.0","scripts":{"build":"nuxt build"}}`)
	write("packages/web/package.json", `{"name":"@acme/web","version":"1.0.0","scripts":{"typecheck":"tsc --noEmit"}}`)

	defer func(p bool) { dryRun = p }(dryRun)
	dryRun = true

	cmd, buf := plainCmd(t)
	if err := runAcross(cmd, root, "typecheck", "", nil); err != nil {
		t.Fatalf("missing script in one package must not abort the run: %v", err)
	}
	out := buf.String()
	if n := strings.Count(out, "pnpm run typecheck"); n != 2 {
		t.Errorf("want typecheck run in the 2 packages that declare it, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, `@acme/docs`) || !strings.Contains(out, `skipped: no "typecheck" script`) {
		t.Errorf("output should report @acme/docs as skipped:\n%s", out)
	}
}
