// Port of the .NET rig's IntegrationTests for custom commands: spawn a real
// process through runCustom and verify the wiring — exit-code propagation for
// the shell and argv forms, passthrough-arg forwarding, per-command env, and
// the clean missing-OS-spec error. The .NET suite's real-`dotnet`-build E2E is
// intentionally not ported: Go's build verb is the ecosystem-generic runner
// (covered by unit tests) and spawning the SDK would dominate the suite's
// runtime. The shell form now defaults to the in-process portable shell, so it
// runs identically on Windows; argv fixtures still spawn the OS shell directly,
// with cmd-flavored variants where syntax differs.
package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/spf13/cobra"
)

// newRunHost builds a bare command to host runCustom (output captured).
func newRunHost() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	return cmd, &buf
}

// exitCode extracts the child's exit code from runCustom's error (0 on nil).
// Both shell paths expose ExitCode() — *exec.ExitError from the OS-shell/argv
// form, *exitError from the portable shell — so assert on the interface, not a
// concrete type.
func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("want an error exposing ExitCode(), got %T: %v", err, err)
	}
	return coder.ExitCode()
}

// shArgv builds an argv-form fixture that exits with code via the OS shell.
func shArgv(script, winScript string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/d", "/s", "/c", winScript}
	}
	return []string{"sh", "-c", script}
}

func TestCustomShellCommand_PropagatesTheExitCode(t *testing.T) {
	// `exit 3` is valid in both sh and cmd.
	def := &config.Command{Spec: &config.CommandSpec{Shell: "exit 3"}}

	host, _ := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "boom", def, nil)

	if got := exitCode(t, err); got != 3 {
		t.Fatalf("exit code = %d, want 3 (a custom shell command's exit code becomes rig's)", got)
	}
}

func TestCustomShellCommand_AppendsPassthroughArgs(t *testing.T) {
	// The passthrough arg must reach the shell line. Quoting differs per OS
	// (posix single-quote vs cmd caret-escape), so assert via echoed output.
	def := &config.Command{Spec: &config.CommandSpec{Shell: "echo rig-arg:"}}

	host, buf := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "say", def, []string{"hello-passthrough"})

	if got := exitCode(t, err); got != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "hello-passthrough") {
		t.Fatalf("output missing the forwarded arg:\n%s", buf.String())
	}
}

func TestCustomArgvCommand_ExecsDirectlyAndPropagatesExitCode(t *testing.T) {
	def := &config.Command{Spec: &config.CommandSpec{Argv: shArgv("exit 5", "exit 5")}}

	host, _ := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "x", def, nil)

	if got := exitCode(t, err); got != 5 {
		t.Fatalf("exit code = %d, want 5 (argv form bypasses the shell yet still propagates)", got)
	}
}

func TestCustomCommandEnv_ReachesTheChildProcess(t *testing.T) {
	// exits with the value of an env var rig injects ($VAR in sh, %VAR% in cmd)
	def := &config.Command{
		Spec: &config.CommandSpec{Argv: shArgv("exit $RIG_TC", "exit %RIG_TC%")},
		Env:  map[string]string{"RIG_TC": "6"},
	}

	host, _ := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "x", def, nil)

	if got := exitCode(t, err); got != 6 {
		t.Fatalf("exit code = %d, want 6 (per-command env must reach the child)", got)
	}
}

func TestCustomCommandWithNoSpecForThisOS_ErrorsCleanly(t *testing.T) {
	def := &config.Command{OS: map[string]*config.CommandSpec{"plan9": {Shell: "true"}}}

	host, _ := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "x", def, nil)

	if err == nil || !strings.Contains(err.Error(), "no command defined for this OS") {
		t.Fatalf("err = %v, want a clean no-spec-for-this-OS error", err)
	}
}

// By default a shell-string command runs through the in-process portable shell,
// so POSIX syntax that cmd.exe can't parse (here $((…)) arithmetic) works on
// every OS with no per-OS variant — the whole point of portable-by-default.
func TestCustomShellCommand_PortableByDefault(t *testing.T) {
	def := &config.Command{Spec: &config.CommandSpec{Shell: "echo sum=$((2 + 3))"}}

	host, buf := newRunHost()
	if err := runCustom(host, config.Config{}, t.TempDir(), "calc", def, nil); err != nil {
		t.Fatalf("run failed: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "sum=5") {
		t.Fatalf("portable shell did not evaluate POSIX arithmetic:\n%s", buf.String())
	}
}

// The portable shell ships cp/mv/rm/mkdir, so a custom command's file ops work
// cross-platform without a Unix userland.
func TestCustomShellCommand_PortableFileOps(t *testing.T) {
	dir := t.TempDir()
	def := &config.Command{Spec: &config.CommandSpec{Shell: "mkdir -p made/here"}}

	host, buf := newRunHost()
	if err := runCustom(host, config.Config{}, dir, "mk", def, nil); err != nil {
		t.Fatalf("run failed: %v\n%s", err, buf.String())
	}
	if info, err := os.Stat(filepath.Join(dir, "made", "here")); err != nil || !info.IsDir() {
		t.Fatalf("portable mkdir -p did not create the nested dir: %v", err)
	}
}

// "shell": "system" opts a command back into the OS shell; the exit code still
// propagates (exercising the non-portable branch).
func TestCustomShellCommand_SystemModePropagatesExitCode(t *testing.T) {
	def := &config.Command{Spec: &config.CommandSpec{Shell: "exit 4"}, Shell: "system"}

	host, _ := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "boom", def, nil)
	if got := exitCode(t, err); got != 4 {
		t.Fatalf("exit code = %d, want 4 (system-mode shell still propagates)", got)
	}
}

// A command's own `shell` overrides the config-level default: here the config
// says system but the command says portable, so POSIX arithmetic runs.
func TestCustomShellCommand_PerCommandShellOverridesConfig(t *testing.T) {
	def := &config.Command{Spec: &config.CommandSpec{Shell: "echo p=$((6 * 7))"}, Shell: "portable"}

	host, buf := newRunHost()
	if err := runCustom(host, config.Config{Shell: "system"}, t.TempDir(), "calc", def, nil); err != nil {
		t.Fatalf("run failed: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "p=42") {
		t.Fatalf("per-command portable override did not take effect:\n%s", buf.String())
	}
}

// An unknown shell value fails the command with a clear error rather than
// silently changing behavior.
func TestCustomShellCommand_InvalidShellErrors(t *testing.T) {
	def := &config.Command{Spec: &config.CommandSpec{Shell: "echo hi"}}

	host, _ := newRunHost()
	err := runCustom(host, config.Config{Shell: "fish"}, t.TempDir(), "x", def, nil)
	if err == nil || !strings.Contains(err.Error(), "shell") {
		t.Fatalf("err = %v, want an unknown-shell error", err)
	}
}

// ---- Tengo script form ----

// A script command runs through the shared runtime: ctx.args is exposed and
// log() reaches the command output.
func TestCustomScript_RunsWithCtxArgsAndLog(t *testing.T) {
	def := &config.Command{Script: &config.ScriptSpec{Code: `log("hello " + ctx.args[0])`}}

	host, buf := newRunHost()
	if err := runCustom(host, config.Config{}, t.TempDir(), "greet", def, []string{"world"}); err != nil {
		t.Fatalf("run: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "hello world") {
		t.Fatalf("output = %q, want it to contain 'hello world'", buf.String())
	}
}

// sh() runs through the portable shell (POSIX arithmetic works on every OS) and
// the file-op builtins touch the disk — a script command is cross-platform.
func TestCustomScript_CrossPlatformShAndFileOps(t *testing.T) {
	dir := t.TempDir()
	def := &config.Command{Script: &config.ScriptSpec{Code: "mkdir(\"-p\", \"built\")\nlog(sh(\"echo $((3 + 4))\"))"}}

	host, buf := newRunHost()
	if err := runCustom(host, config.Config{}, dir, "b", def, nil); err != nil {
		t.Fatalf("run: %v\n%s", err, buf.String())
	}
	if info, err := os.Stat(filepath.Join(dir, "built")); err != nil || !info.IsDir() {
		t.Fatalf("mkdir() did not create the dir: %v", err)
	}
	if !strings.Contains(buf.String(), "7") {
		t.Fatalf("sh() arithmetic output missing: %q", buf.String())
	}
}

// ctx exposes the environment, repo root, OS, and resolved ecosystem.
func TestCustomScript_CtxExposesEnvRootOsEcosystem(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code := "log(ctx.os)\nlog(ctx.root)\nlog(ctx.ecosystem)\nlog(ctx.env.RIG_TC)"
	def := &config.Command{Script: &config.ScriptSpec{Code: code}, Env: map[string]string{"RIG_TC": "yes"}}

	host, buf := newRunHost()
	if err := runCustom(host, config.Config{}, dir, "ctx", def, nil); err != nil {
		t.Fatalf("run: %v\n%s", err, buf.String())
	}
	out := buf.String()
	for _, want := range []string{runtime.GOOS, dir, "go", "yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("ctx output missing %q:\n%s", want, out)
		}
	}
}

// fail() aborts the script and fails the command.
func TestCustomScript_FailAborts(t *testing.T) {
	def := &config.Command{Script: &config.ScriptSpec{Code: `fail("nope")`}}

	host, _ := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "x", def, nil)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want the fail message", err)
	}
}

// A command can't set both a command/argv form and a script form.
func TestCustomScript_RejectsCommandAndScriptTogether(t *testing.T) {
	def := &config.Command{
		Spec:   &config.CommandSpec{Shell: "echo hi"},
		Script: &config.ScriptSpec{Code: `log("x")`},
	}

	host, _ := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "x", def, nil)
	if err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("err = %v, want a both-forms error", err)
	}
}

// A dry run previews sh()/file ops (logic still runs, disk untouched).
func TestCustomScript_DryRunPreviewsSideEffects(t *testing.T) {
	dir := t.TempDir()
	def := &config.Command{Script: &config.ScriptSpec{Code: "mkdir(\"-p\", \"dist\")\nsh(\"echo build\")"}}

	prev := dryRun
	dryRun = true
	defer func() { dryRun = prev }()

	host, buf := newRunHost()
	if err := runCustom(host, config.Config{}, dir, "x", def, nil); err != nil {
		t.Fatalf("run: %v\n%s", err, buf.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "dist")); !os.IsNotExist(err) {
		t.Fatal("mkdir() must not touch the disk in a dry run")
	}
	out := buf.String()
	if !strings.Contains(out, "would mkdir -p dist") || !strings.Contains(out, "would run: echo build") {
		t.Fatalf("dry-run previews missing:\n%s", out)
	}
}

// The { "file": … } form loads the .tengo file relative to the config and runs
// it; a missing file fails cleanly when the command runs.
func TestCustomScript_FileForm(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cmd.tengo"), []byte(`log("from file")`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rig.json"),
		[]byte(`{ "commands": { "fromfile": { "script": { "file": "cmd.tengo" } }, "missing": { "script": { "file": "nope.tengo" } } } }`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	host, buf := newRunHost()
	if err := runCustom(host, cfg, dir, "fromfile", cfg.Commands["fromfile"], nil); err != nil {
		t.Fatalf("file-form run: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "from file") {
		t.Fatalf("file-form script did not run: %q", buf.String())
	}

	host2, _ := newRunHost()
	err = runCustom(host2, cfg, dir, "missing", cfg.Commands["missing"], nil)
	if err == nil || !strings.Contains(err.Error(), "could not be loaded") {
		t.Fatalf("missing-file err = %v, want a clean load error", err)
	}
}

// A multi-binary Go repo (mains under cmd/) must expand into one run target per
// binary, never the unrunnable module root. Library packages are not run targets;
// scripts/ mains stay in the set (runnable by name) and are deduped into the
// Scripts group by the picker, not here.
func TestRunTargets_ExpandsGoModuleIntoBinaries(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/app\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGoPkg(t, root, "cmd/api", "main")
	writeGoPkg(t, root, "cmd/worker", "main")
	writeGoPkg(t, root, "internal/lib", "lib") // a library — not runnable
	writeGoPkg(t, root, "scripts/gen", "main") // a main, but a script verb

	got := map[string]string{} // binary name -> rel dir
	for _, tg := range runTargets(context.Background(), root) {
		rel, _ := filepath.Rel(root, tg.Dir)
		got[tg.Name] = filepath.ToSlash(rel)
	}
	if got["api"] != "cmd/api" || got["worker"] != "cmd/worker" {
		t.Errorf("runTargets = %v, want api→cmd/api and worker→cmd/worker", got)
	}
	if _, ok := got["lib"]; ok {
		t.Errorf("a library package must not be a run target: %v", got)
	}
	if got["gen"] != "scripts/gen" {
		t.Errorf("runTargets should include the scripts/gen main (the picker dedups it): %v", got)
	}
}

// A .rig.json `exclude` glob hides an individual binary by name, not just a whole
// module — the user's lever for trimming the run picker.
func TestRunTargets_ExcludeHidesABinary(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/app\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rig.json"),
		[]byte(`{"exclude":["worker"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGoPkg(t, root, "cmd/api", "main")
	writeGoPkg(t, root, "cmd/worker", "main")

	names := map[string]bool{}
	for _, tg := range runTargets(context.Background(), root) {
		names[tg.Name] = true
	}
	if !names["api"] || names["worker"] {
		t.Errorf("runTargets names = %v, want api kept and worker excluded", names)
	}
}

// writeGoPkg creates root/rel with a single .go file declaring `package pkg`.
func writeGoPkg(t *testing.T, root, rel, pkg string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package " + pkg + "\n"
	if pkg == "main" {
		src += "func main() {}\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cmdNames(cmds []*cobra.Command) []string {
	names := make([]string, len(cmds))
	for i, c := range cmds {
		names[i] = c.Name()
	}
	sort.Strings(names)
	return names
}

func TestGoScriptCmds_SurfacesScriptsOnDiskAndGoWorkCmd(t *testing.T) {
	root := t.TempDir()
	// scripts/ mains are surfaced directly from disk (no go.work entry needed),
	// so a single-module repo still gets its helper verbs.
	writeGoPkg(t, root, "scripts/dev-install", "main")
	writeGoPkg(t, root, "scripts/undeclared", "main") // not in go.work, still surfaces
	// A cmd/ main surfaces only when opted in via go.work...
	writeGoPkg(t, root, "cmd/gen", "main")
	// ...whereas a cmd/ main only on disk (not in go.work) is NOT auto-scanned:
	// cmd/ holds product binaries, kept out of rig's verb space.
	writeGoPkg(t, root, "cmd/ondisk", "main")
	// Excluded regardless: a library (not main), a main outside scripts//cmd/,
	// and a main whose name collides with a built-in verb.
	writeGoPkg(t, root, "scripts/lib", "lib")
	writeGoPkg(t, root, "tools/x", "main")
	writeGoPkg(t, root, "scripts/run", "main")
	goWork := "go 1.26\n\nuse (\n\t./scripts/dev-install\n\t./cmd/gen\n\t./scripts/lib\n\t./tools/x\n\t./scripts/run\n)\n"
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte(goWork), 0o644); err != nil {
		t.Fatal(err)
	}

	cmds := goScriptCmds(root)
	if got, want := cmdNames(cmds), []string{"dev-install", "gen", "undeclared"}; !slicesEqual(got, want) {
		t.Fatalf("verbs = %v, want %v (scripts/ from disk + cmd/ via go.work; no libs/builtins/cmd-on-disk/non-conventional)", got, want)
	}
	for _, c := range cmds {
		if c.Annotations[scriptVerbAnnotation] == "" {
			t.Errorf("%q missing the script-verb annotation (needed to keep it out of prefix-matching)", c.Name())
		}
	}
}

func TestGoScriptCmds_NoGoWorkIsNoOp(t *testing.T) {
	if cmds := goScriptCmds(t.TempDir()); cmds != nil {
		t.Fatalf("want nil without a go.work, got %d cmd(s)", len(cmds))
	}
}

func TestGoScriptCmd_RunsGoRunFromRoot(t *testing.T) {
	root := t.TempDir()
	writeGoPkg(t, root, "scripts/dev-install", "main")
	if err := os.WriteFile(filepath.Join(root, "go.work"),
		[]byte("go 1.26\n\nuse ./scripts/dev-install\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmds := goScriptCmds(root)
	if len(cmds) != 1 {
		t.Fatalf("want 1 verb, got %d", len(cmds))
	}
	// --dry-run echoes the command instead of spawning the toolchain.
	defer func(p bool) { dryRun = p }(dryRun)
	dryRun = true
	host, buf := newRunHost()
	if err := cmds[0].RunE(host, []string{"--flag"}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "go run ./scripts/dev-install --flag") {
		t.Fatalf("echo = %q, want it to run `go run ./scripts/dev-install` with forwarded args", got)
	}
}

func TestIsGoMainPackage(t *testing.T) {
	root := t.TempDir()
	writeGoPkg(t, root, "ismain", "main")
	writeGoPkg(t, root, "islib", "lib")
	if !isGoMainPackage(filepath.Join(root, "ismain")) {
		t.Error("package main not detected as runnable")
	}
	if isGoMainPackage(filepath.Join(root, "islib")) {
		t.Error("a library package wrongly detected as runnable")
	}
	if isGoMainPackage(filepath.Join(root, "missing")) {
		t.Error("a missing dir wrongly detected as runnable")
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestShellInvocationShapes(t *testing.T) {
	display, argv := shellInvocation("echo hi", []string{"a b"})
	if runtime.GOOS == "windows" {
		if argv[0] != "cmd.exe" || argv[1] != "/d" || argv[3] != "/c" {
			t.Fatalf("windows argv = %v, want cmd.exe /d /s /c", argv)
		}
		// The forwarded arg is appended caret-escaped (winShellArg), so the
		// raw "a b" won't appear verbatim — the space is escaped to "a^ b".
		if want := "echo hi " + winShellArg("a b"); display != want {
			t.Fatalf("windows display = %q, want %q", display, want)
		}
		return
	}
	if argv[0] != "sh" || argv[1] != "-c" || argv[2] != "echo hi 'a b'" {
		t.Fatalf("posix argv = %v", argv)
	}
	if display != "echo hi 'a b'" {
		t.Fatalf("display = %q", display)
	}
}
