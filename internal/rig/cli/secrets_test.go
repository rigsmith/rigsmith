package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rigsmith/rigsmith/core/secrets"
	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/spf13/cobra"
)

// plantedSecret is a value no rig output may ever contain.
const plantedSecret = "pg-password-never-printed"

// stubDoppler is a provider whose readiness and values a test sets; its Wrap
// is the real Doppler's, so the line under test is the one rig would run.
type stubDoppler struct {
	ready  error
	values map[string]string
	reads  int
}

func (s *stubDoppler) Name() string  { return "doppler" }
func (s *stubDoppler) Title() string { return "Doppler" }
func (s *stubDoppler) Ready(context.Context, string) error {
	return s.ready
}
func (s *stubDoppler) Wrap(scope secrets.Scope, argv []string) []string {
	return secrets.NewDoppler().Wrap(scope, argv)
}
func (s *stubDoppler) CISteps(o secrets.CIOptions) (string, error) {
	return secrets.NewDoppler().CISteps(o)
}
func (s *stubDoppler) Get(_ context.Context, _ string, _ secrets.Scope, name string) (string, error) {
	return s.values[name], nil
}
func (s *stubDoppler) All(context.Context, string, secrets.Scope) (map[string]string, error) {
	s.reads++
	return s.values, nil
}

// dopplerRepo points rig at a repo whose .rig.json holds body, with the
// provider stubbed and CI pinned off, and restores everything after.
func dopplerRepo(t *testing.T, body string, stub *stubDoppler) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIG_GLOBAL_CONFIG", filepath.Join(t.TempDir(), "absent.json"))
	prevRoot, prevCI, prevDry, prevNo, prevQuiet := rootFlag, secrets.InCI, dryRun, noSecrets, quiet
	rootFlag, secrets.InCI, dryRun, noSecrets, quiet = dir, func() bool { return false }, false, false, false
	readyChecked = map[string]error{}
	restore := secrets.SetProviderForTest("doppler", stub)
	t.Cleanup(func() {
		rootFlag, secrets.InCI, dryRun, noSecrets, quiet = prevRoot, prevCI, prevDry, prevNo, prevQuiet
		readyChecked = map[string]error{}
		restore()
	})
	return dir
}

const foxcraftDev = `{ "doppler": { "project": "foxcraft", "dev": "dev", "ci": "ci" } }`

func verbHost(name string) (*cobra.Command, *strings.Builder) {
	root := &cobra.Command{Use: "rig"}
	cmd := &cobra.Command{Use: name}
	// Not isBuiltinVerbName: its first call resets the flag globals.
	if builtin := map[string]bool{"format": true, "install": true, "outdated": true}; !secretVerbs[name] && !builtin[name] {
		// A surfaced custom command or script, as scriptEntryCmds builds it.
		cmd.Annotations = map[string]string{runsProjectCodeAnnotation: "true"}
	}
	root.AddCommand(cmd)
	cmd.SetContext(context.Background())
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	return cmd, &out
}

func TestDryRunEchoesTheWrappedCommand(t *testing.T) {
	dir := dopplerRepo(t, foxcraftDev, &stubDoppler{ready: errors.New("never asked in a dry run")})
	dryRun = true
	cmd, out := verbHost("test")
	if err := runCommand(cmd, dir, []string{"go", "test", "./..."}); err != nil {
		t.Fatal(err)
	}
	if want := "→ doppler run -p foxcraft -c dev -- go test ./..."; !strings.Contains(out.String(), want) {
		t.Fatalf("echo = %q, want %q", out.String(), want)
	}
}

func TestOnlyVerbsThatRunCodeAreWrapped(t *testing.T) {
	dir := dopplerRepo(t, foxcraftDev, &stubDoppler{})
	for verb, wrapped := range map[string]bool{
		"test": true, "run": true, "build": true, "coverage": true, "my-custom": true,
		"format": false, "install": false, "outdated": false,
	} {
		cmd, _ := verbHost(verb)
		argv, err := withSecrets(cmd, dir, []string{"x"})
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		if got := argv[0] == "doppler"; got != wrapped {
			t.Errorf("%s wrapped = %v, want %v (%v)", verb, got, wrapped, argv)
		}
	}
}

func TestNoDopplerAndCISkipTheWrap(t *testing.T) {
	dir := dopplerRepo(t, foxcraftDev, &stubDoppler{ready: errors.New("must not be asked")})
	cmd, _ := verbHost("test")

	noSecrets = true
	if argv, err := withSecrets(cmd, dir, []string{"go", "test"}); err != nil || argv[0] != "go" {
		t.Fatalf("--no-doppler: %v, %v", argv, err)
	}
	noSecrets = false

	secrets.InCI = func() bool { return true }
	if argv, err := withSecrets(cmd, dir, []string{"go", "test"}); err != nil || argv[0] != "go" {
		t.Fatalf("in CI the workflow supplies secrets, rig wraps nothing: %v, %v", argv, err)
	}
}

func TestNoBlockOrNoDevConfigChangesNothing(t *testing.T) {
	for name, body := range map[string]string{
		"no block":      `{ "quiet": false }`,
		"no dev config": `{ "doppler": { "project": "foxcraft", "ci": "ci" } }`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := dopplerRepo(t, body, &stubDoppler{ready: errors.New("must not be asked")})
			cmd, _ := verbHost("test")
			if argv, err := withSecrets(cmd, dir, []string{"go", "test"}); err != nil || argv[0] != "go" {
				t.Fatalf("withSecrets = %v, %v", argv, err)
			}
		})
	}
}

func TestRefusesWhenTheCLIIsMissingOrSignedOut(t *testing.T) {
	for name, readyErr := range map[string]error{
		"missing":    errors.New("the doppler CLI is not installed — install it: " + secrets.ErrNotInstalled.Error()),
		"signed out": errors.New("the doppler CLI is not signed in — run `doppler login`"),
	} {
		t.Run(name, func(t *testing.T) {
			dir := dopplerRepo(t, foxcraftDev, &stubDoppler{ready: readyErr})
			cmd, out := verbHost("test")
			err := runCommand(cmd, dir, []string{"go", "version"})
			if err == nil {
				t.Fatal("rig ran without secrets instead of refusing")
			}
			if !strings.Contains(err.Error(), readyErr.Error()) || !strings.Contains(err.Error(), "--no-doppler") {
				t.Errorf("error = %q, want the CLI's fix and the --no-doppler way out", err)
			}
			if strings.Contains(out.String(), "→") {
				t.Errorf("a refused run must not echo a command: %q", out.String())
			}
		})
	}
}

func TestABrokenBlockIsAnErrorNotAnOptOut(t *testing.T) {
	dir := dopplerRepo(t, `{ "doppler": { "dev": "dev" } }`, &stubDoppler{})
	cmd, _ := verbHost("test")
	if _, err := withSecrets(cmd, dir, []string{"go", "test"}); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("withSecrets = %v, want the missing project named", err)
	}
}

func TestInProcessCommandGetsSecretsButNeverPrintsThem(t *testing.T) {
	stub := &stubDoppler{values: map[string]string{"PG_URL": plantedSecret}}
	dir := dopplerRepo(t, foxcraftDev, stub)
	def := &config.Command{Spec: &config.CommandSpec{Shell: `[ -n "$PG_URL" ] && echo has-secret`}}
	cmd, out := verbHost("migrate")
	if err := runCustom(cmd, config.Config{}, dir, "migrate", def, nil); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	got := out.String()
	if !strings.Contains(got, "has-secret") {
		t.Errorf("the secret did not reach the command:\n%s", got)
	}
	if !strings.Contains(got, "→ [doppler foxcraft/dev] ") {
		t.Errorf("the echo does not say the run reads Doppler:\n%s", got)
	}
	if strings.Contains(got, plantedSecret) {
		t.Errorf("the value reached the output:\n%s", got)
	}

	// A dry run names the source and reads nothing.
	stub.reads = 0
	dryRun = true
	out.Reset()
	if err := runCustom(cmd, config.Config{}, dir, "migrate", def, nil); err != nil {
		t.Fatal(err)
	}
	if stub.reads != 0 || strings.Contains(out.String(), plantedSecret) || !strings.Contains(out.String(), "[doppler foxcraft/dev]") {
		t.Errorf("dry run read %d times and printed:\n%s", stub.reads, out.String())
	}
}

func TestArgvCustomCommandIsWrapped(t *testing.T) {
	dir := dopplerRepo(t, foxcraftDev, &stubDoppler{})
	dryRun = true
	def := &config.Command{Spec: &config.CommandSpec{Argv: []string{"psql", "-c", "select 1"}}}
	cmd, out := verbHost("db")
	if err := runCustom(cmd, config.Config{}, dir, "db", def, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "→ doppler run -p foxcraft -c dev -- ") {
		t.Fatalf("echo = %q", out.String())
	}
}

func TestSecretsCIPrintsStepsForTheCIConfig(t *testing.T) {
	dopplerRepo(t, foxcraftDev, &stubDoppler{})
	cmd := newSecretsCmd()
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"ci", "POSTGRES_URL"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "doppler-config: ci") || !strings.Contains(out.String(), "@"+secrets.DopplerFetchActionSHA) {
		t.Errorf("stdout is not the pinned fetch for foxcraft/ci:\n%s", out.String())
	}
	if strings.Contains(out.String(), "then, on each step") {
		t.Error("the guidance must go to stderr, so stdout is only the steps")
	}
	if !strings.Contains(errOut.String(), "POSTGRES_URL: ${{ steps.doppler.outputs.POSTGRES_URL }}") {
		t.Errorf("stderr lacks the consuming step's env:\n%s", errOut.String())
	}
}

// Deciding whether a verb runs under Doppler must not rebuild the root command:
// that re-registers the persistent flags and resets -n mid-run, so a dry run
// would really run. Reproduced by forcing the lazily built verb list to be
// built during the decision, as it is in a repo with no custom commands.
func TestDecidingTheWrapLeavesDryRunAlone(t *testing.T) {
	dir := dopplerRepo(t, foxcraftDev, &stubDoppler{})
	// A custom command: a built-in like "test" is decided before the verb list
	// is ever consulted, so it can't show the reset.
	cmd, out := verbHost("my-custom")
	builtinVerbsOnce = sync.Once{}
	dryRun, quiet, noSecrets = true, false, false
	if err := runCommand(cmd, dir, []string{"false"}); err != nil {
		t.Fatalf("a dry run executed the command: %v", err)
	}
	if !dryRun {
		t.Fatal("dryRun was reset while deciding the wrap")
	}
	if !strings.Contains(out.String(), "→ doppler run -p foxcraft -c dev -- false") {
		t.Errorf("echo = %q", out.String())
	}
}

// A .rig.json that exists but cannot be read may hold the provider block, so
// it is an error, never a run without secrets.
func TestAnUnreadableConfigIsAnErrorNotAnOptOut(t *testing.T) {
	dir := dopplerRepo(t, foxcraftDev, &stubDoppler{})
	path := filepath.Join(dir, config.FileName)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil { // reads fail, but it is there
		t.Fatal(err)
	}
	cmd, _ := verbHost("test")
	if argv, err := withSecrets(cmd, dir, []string{"go", "test"}); err == nil {
		t.Fatalf("withSecrets = %v, nil; an unreadable config must stop the run", argv)
	}
}

// A malformed .rig.json that names Doppler may hold the block rig needs, so
// the run refuses rather than going on without secrets.
func TestAMalformedConfigNamingDopplerIsAnError(t *testing.T) {
	dir := dopplerRepo(t, `{ "doppler": { "project": "foxcraft", "dev": "dev" }`, &stubDoppler{}) // no closing brace
	cmd, _ := verbHost("test")
	if _, err := withSecrets(cmd, dir, []string{"go", "test"}); err == nil {
		t.Fatal("a broken file naming doppler ran without secrets")
	}
}

// --all leaves a package that doesn't define the verb alone: it never runs,
// so its folder needs no Doppler sign-in.
func TestAllTasksWrapOnlyWhatRuns(t *testing.T) {
	dir := dopplerRepo(t, foxcraftDev, &stubDoppler{})
	dryRun = true
	cmd, _ := verbHost("test")
	got, err := wrapAllTasks(cmd, []allTask{
		{name: "app", dir: dir, argv: []string{"go", "test"}},
		{name: "docs", dir: dir, argv: []string{"npm", "test"}, skip: "no test script"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].argv[0] != "doppler" {
		t.Errorf("runnable task argv = %v, want it wrapped", got[0].argv)
	}
	if got[1].argv[0] != "npm" {
		t.Errorf("skipped task argv = %v, want it untouched", got[1].argv)
	}
}
