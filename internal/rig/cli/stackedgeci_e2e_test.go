package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStackEdgeCIReportDotnetE2E(t *testing.T) {
	requireStackEdgeEngine(t)
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
	defer cancel()
	f := newEdgeCITestFixture(t)
	wantMoves := f.adoptedMoves(t)
	t.Setenv("EDGE_FEED_KEY", "")
	t.Setenv("RIG_STACK_CACHE", "")
	exe := filepath.Join(t.TempDir(), "rig"+stackExeSuffix())
	build := exec.CommandContext(ctx, "go", "build", "-o", exe, "../../../cmd/rig")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build rig: %s: %v", b, err)
	}
	old := stackCIExecutable
	stackCIExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { stackCIExecutable = old })
	f.o.Publish = ""
	f.o.Report = filepath.Join(t.TempDir(), "edge.json")
	f.o.Number = 7
	f.o.Build = `printf 'untrusted build stdout'; printf 'untrusted build stderr' >&2; dotnet build lib/Lib.csproj -c Release --nologo; printf build > build-ran`
	f.o.Test = `printf 'untrusted test stdout'; printf 'untrusted test stderr' >&2; test -f build-ran && test -f lib/fix.txt && test ! -f lib/dirty.txt`
	var diagnostics bytes.Buffer
	f.o.Stderr = &diagnostics
	// Poison the user's checkout; the seed and fork must still select inputs.
	if err := os.WriteFile(filepath.Join(f.checkout, "rig.stack.jsonc"), []byte("invalid dirty manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(f.checkout)
	var out bytes.Buffer
	if err := stackEdgeCI(ctx, &out, f.o); err != nil {
		t.Fatalf("report: %v; %s", err, out.String())
	}
	r, err := stackEdgeCIReadReport(f.o.Report)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "packed" || r.Tip || r.Number != 7 || r.Pack.Commit != f.track || r.Pack.Version != "1.2.4-edge.7" || len(r.SHA256) != 1 {
		t.Fatalf("report=%+v", r)
	}
	if len(r.AdoptedMoved) != 1 || r.AdoptedMoved[0].PR != wantMoves[0].PR || r.AdoptedMoved[0].Warning == "" || !strings.Contains(out.String(), r.AdoptedMoved[0].Warning) {
		t.Fatalf("moved PR warning missing: %+v; %s", r.AdoptedMoved, &out)
	}
	b, err := os.ReadFile(f.o.Report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "untrusted ") || strings.Contains(out.String(), "untrusted ") || strings.Contains(string(b), edgeCIKey) {
		t.Fatal("report contains subprocess output or credential")
	}
	for _, marker := range []string{"untrusted build stdout", "untrusted build stderr", "untrusted test stdout", "untrusted test stderr"} {
		if !strings.Contains(diagnostics.String(), marker) {
			t.Fatalf("missing diagnostic %q in stderr: %s", marker, diagnostics.String())
		}
	}
	assertStackEdgeFile(t, f.checkout, "rig.stack.jsonc", "invalid dirty manifest")
	// The same actual .NET artifacts must survive the independent publisher.
	f.o.Publish, f.o.Report = f.o.Report, ""
	t.Setenv("EDGE_FEED_KEY", edgeCIKey)
	stackCIExecutable = func() (string, error) { return "", fmt.Errorf("publisher tried to find project executable") }
	if err := stackEdgeCI(ctx, &out, f.o); err != nil {
		t.Fatal(err)
	}
	if len(f.f.pushes) != 1 {
		t.Fatal("real pack did not publish")
	}
}

func TestStackEdgeCIReportFailureE2E(t *testing.T) {
	requireStackEdgeEngine(t)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
	defer cancel()
	f := newEdgeCITestFixture(t)
	t.Setenv("EDGE_FEED_KEY", "")
	t.Setenv("RIG_STACK_CACHE", "")
	exe := filepath.Join(t.TempDir(), "rig"+stackExeSuffix())
	build := exec.CommandContext(ctx, "go", "build", "-o", exe, "../../../cmd/rig")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build rig: %s: %v", b, err)
	}
	old := stackCIExecutable
	stackCIExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { stackCIExecutable = old })
	report := filepath.Join(t.TempDir(), "edge.json")
	const previous = "{\"status\":\"previous\"}\n"
	if err := os.WriteFile(report, []byte(previous), 0600); err != nil {
		t.Fatal(err)
	}
	c := newStackEdgeCICmd()
	c.SetContext(ctx)
	c.SilenceErrors, c.SilenceUsage = true, true
	var stdout, stderr bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(&stderr)
	c.SetArgs([]string{"--seed", f.o.Seed, "--member", "lib", "--number", "7", "--allowed-hosts", f.o.Allowed, "--report", report, "-o", f.o.Out,
		"--build", `printf 'failing-build-stdout'; printf 'failing-build-stderr' >&2; exit 7`, "--test", `printf 'test-must-not-run'`})
	err := c.Execute()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || !strings.Contains(err.Error(), "edge build failed") {
		t.Fatalf("build error was not preserved: %v", err)
	}
	for _, marker := range []string{"failing-build-stdout", "failing-build-stderr"} {
		if !strings.Contains(stderr.String(), marker) || strings.Contains(stdout.String(), marker) {
			t.Fatalf("diagnostic %q must reach only command stderr; stdout=%q stderr=%q", marker, stdout.String(), stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "test-must-not-run") || strings.Contains(stderr.String(), edgeCIKey) {
		t.Fatal("failed build ran tests or leaked a credential")
	}
	assertStackEdgeFile(t, filepath.Dir(report), filepath.Base(report), previous)
}

func TestStackEdgeCIReportInconclusiveE2E(t *testing.T) {
	requireStackEdgeEngine(t)
	f := newEdgeCITestFixture(t)
	t.Setenv("EDGE_FEED_KEY", "")
	t.Setenv("RIG_STACK_CACHE", "")
	f.g.commit(t, "acme/lib", "fix.txt", "conflicting upstream\n", "advance upstream")
	f.o.Publish = ""
	f.o.Report = filepath.Join(t.TempDir(), "edge.json")
	f.o.Number = 8
	f.o.Tip = true
	if err := os.WriteFile(filepath.Join(f.o.Out, "stale.nupkg"), []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	old := stackCIExecutable
	stackCIExecutable = func() (string, error) { t.Fatal("conflict executed project code"); return "", nil }
	t.Cleanup(func() { stackCIExecutable = old })
	var out bytes.Buffer
	if err := stackEdgeCI(t.Context(), &out, f.o); err != nil {
		t.Fatal(err)
	}
	r, err := stackEdgeCIReadReport(f.o.Report)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "inconclusive" || !r.Tip || r.Pack.Provenance["lib"].TrackCommit != f.track || r.Pack.Provenance["lib"].UpstreamTip == "" || r.Pack.Remedy == "" || len(r.SHA256) != 0 {
		t.Fatalf("report=%+v", r)
	}
	if _, err := os.Stat(filepath.Join(f.o.Out, "stale.nupkg")); !os.IsNotExist(err) {
		t.Fatal("inconclusive retained stale package")
	}
	if len(f.f.pushes) != 0 {
		t.Fatal("inconclusive reached feed")
	}
}
