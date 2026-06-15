// Ported from net-changesets Release/ReleasePipelineTests.cs (28 tests).
package pipeline

import (
	"runtime"
	"strings"
	"testing"
)

type fixture struct {
	runner   *recordingRunner
	reporter *recordingReporter
	masker   *SecretMasker
	prompter *stubPrompter
	pipeline *Pipeline
}

func newFixture(native map[string]NativeHandler) *fixture {
	f := &fixture{
		runner:   &recordingRunner{},
		reporter: &recordingReporter{},
		masker:   NewSecretMasker(),
		prompter: &stubPrompter{answer: true},
	}
	f.pipeline = New(f.runner.run, f.reporter, f.masker, f.prompter, "/tmp/repo", nil, native)
	return f
}

func mustResolve(t *testing.T, config *Config, opts ResolveOptions) []ResolvedStep {
	t.Helper()
	steps, err := Resolve(config, opts)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	return steps
}

func findStep(t *testing.T, steps []ResolvedStep, name string) ResolvedStep {
	t.Helper()
	for _, step := range steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("step %q not found in %v", name, stepNames(steps))
	return ResolvedStep{}
}

func stepNames(steps []ResolvedStep) []string {
	names := make([]string, len(steps))
	for i, step := range steps {
		names[i] = step.Name
	}
	return names
}

func equalStrings(a, b []string) bool {
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

func boolPtr(b bool) *bool { return &b }

func strPtr(s string) *string { return &s }

func singleShellAction(t *testing.T, step ResolvedStep) string {
	t.Helper()
	if len(step.Action) != 1 {
		t.Fatalf("step %q action = %d commands, want 1", step.Name, len(step.Action))
	}
	if !step.Action[0].IsShell() {
		t.Fatalf("step %q action is not a shell command", step.Name)
	}
	return step.Action[0].Shell()
}

func TestResolveUsesDefaultOrderWhenNoneConfigured(t *testing.T) {
	steps := mustResolve(t, &Config{}, ResolveOptions{})

	want := []string{"version", "commit", "build", "publish", "tag", "push", "release", "issues"}
	if !equalStrings(stepNames(steps), want) {
		t.Errorf("names = %v, want %v", stepNames(steps), want)
	}
	for _, step := range steps {
		if !step.Enabled() || !step.IsBuiltin {
			t.Errorf("step %q: enabled=%v builtin=%v, want both true", step.Name, step.Enabled(), step.IsBuiltin)
		}
	}
	if findStep(t, steps, "release").Kind != StepKindNative {
		t.Error("release should be a native step")
	}
	// build runs the host's Artifacts handler — a native step, like release.
	if findStep(t, steps, "build").Kind != StepKindNative {
		t.Error("build should be a native step")
	}
}

func TestResolveDryBuildRunsOnlyBuild(t *testing.T) {
	steps := mustResolve(t, &Config{}, ResolveOptions{DryBuild: true})

	sawBuild := false
	for _, step := range steps {
		if step.Name == "build" {
			sawBuild = true
			if !step.Enabled() {
				t.Errorf("build should run under --dry-build, got skip %q", step.SkipReason)
			}
			continue
		}
		if step.Enabled() {
			t.Errorf("step %q should be skipped under --dry-build", step.Name)
		}
		if step.SkipReason != "dry-build: build only" {
			t.Errorf("step %q skip reason = %q, want %q", step.Name, step.SkipReason, "dry-build: build only")
		}
	}
	if !sawBuild {
		t.Fatal("no build step in the resolved plan")
	}
}

// TestResolveDryBuildTakesPrecedence: dry-build overrides the step-selection
// filters — build always runs and every other step keeps the dry-build reason,
// even when --from/--to/--only/--skip would otherwise skip build or relabel.
func TestResolveDryBuildTakesPrecedence(t *testing.T) {
	opts := ResolveOptions{
		DryBuild: true,
		From:     "publish", // would skip build (it's before publish) without precedence
		To:       "tag",
		Only:     []string{"release"},
		Skip:     []string{"build"},
	}
	steps := mustResolve(t, &Config{}, opts)
	for _, step := range steps {
		if step.Name == "build" {
			if !step.Enabled() {
				t.Errorf("build must run under --dry-build despite other filters, got skip %q", step.SkipReason)
			}
			continue
		}
		if step.SkipReason != "dry-build: build only" {
			t.Errorf("step %q skip reason = %q, want %q", step.Name, step.SkipReason, "dry-build: build only")
		}
	}
}

func TestResolveBuiltinPublishUsesToolAndAppendsArgs(t *testing.T) {
	config := &Config{
		Tool: "npx changeset",
		Steps: map[string]*StepConfig{
			"publish": {Args: []string{"--otp", "${vars.npmOtp}"}},
		},
	}

	publish := findStep(t, mustResolve(t, config, ResolveOptions{}), "publish")

	// In the pipeline, tagging is the `tag` step's job, so publish carries
	// --no-git-tag (before any user args).
	if got := singleShellAction(t, publish); got != "npx changeset publish --no-git-tag --otp ${vars.npmOtp}" {
		t.Errorf("publish action = %q", got)
	}
}

func TestResolveBuiltinTagUsesTool(t *testing.T) {
	config := &Config{Tool: "npx changeset"}
	tag := findStep(t, mustResolve(t, config, ResolveOptions{}), "tag")
	if got := singleShellAction(t, tag); got != "npx changeset tag" {
		t.Errorf("tag action = %q, want %q", got, "npx changeset tag")
	}
	if tag.Kind != StepKindCommands {
		t.Error("tag should be a command step")
	}
}

func TestResolveBuiltinVersionUsesToolAndAppendsArgs(t *testing.T) {
	config := &Config{
		Tool: "npx changeset",
		Steps: map[string]*StepConfig{
			"version": {Args: []string{"--snapshot", "canary"}},
		},
	}

	version := findStep(t, mustResolve(t, config, ResolveOptions{}), "version")

	if got := singleShellAction(t, version); got != "npx changeset version --snapshot canary" {
		t.Errorf("version action = %q", got)
	}
}

func TestResolveBuiltinCommitGuardsAgainstEmptyIndex(t *testing.T) {
	// The commit step stages everything, then commits only when the index is
	// non-empty — an earlier step may have already committed, leaving an empty
	// index that a bare `git commit` would fail on.
	config := &Config{Order: []string{"commit"}}

	commit := findStep(t, mustResolve(t, config, ResolveOptions{}), "commit")

	if len(commit.Action) != 2 {
		t.Fatalf("commit action = %d commands, want 2 (%+v)", len(commit.Action), commit.Action)
	}
	if commit.Action[0].IsShell() {
		t.Errorf("commit action[0] should be an argv command, got shell %q", commit.Action[0].Shell())
	}
	if got := commit.Action[0].Argv(); !equalStrings(got, []string{"git", "add", "-A"}) {
		t.Errorf("commit action[0] argv = %v, want [git add -A]", got)
	}
	if !commit.Action[1].IsShell() {
		t.Fatalf("commit action[1] should be a shell command")
	}
	if got, want := commit.Action[1].Shell(), "git diff --cached --quiet || git commit -m 'chore: release'"; got != want {
		t.Errorf("commit action[1] shell = %q, want %q", got, want)
	}
}

func TestResolveBuiltinCommitEscapesSingleQuoteInMessage(t *testing.T) {
	config := &Config{
		Order: []string{"commit"},
		Steps: map[string]*StepConfig{
			"commit": {Message: strPtr("it's a release")},
		},
	}

	commit := findStep(t, mustResolve(t, config, ResolveOptions{}), "commit")

	if len(commit.Action) != 2 {
		t.Fatalf("commit action = %d commands, want 2", len(commit.Action))
	}
	if !commit.Action[1].IsShell() {
		t.Fatalf("commit action[1] should be a shell command")
	}
	if got, want := commit.Action[1].Shell(), `git diff --cached --quiet || git commit -m 'it'\''s a release'`; got != want {
		t.Errorf("commit action[1] shell = %q, want %q", got, want)
	}
}

func TestRunBuiltinCommitEmptyIndexSucceeds(t *testing.T) {
	// The guard short-circuits the commit when the index is clean; here the
	// shell guard "succeeds" (exit 0), documenting that an empty-index commit no
	// longer fails the release.
	f := newFixture(nil)
	config := &Config{Order: []string{"commit"}}

	success := f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	if !success {
		t.Error("run should succeed even when the commit guard short-circuits")
	}
	want := []string{
		"git add -A",
		"sh -c git diff --cached --quiet || git commit -m 'chore: release'",
	}
	if !equalStrings(f.runner.lines(), want) {
		t.Errorf("calls = %v, want %v", f.runner.lines(), want)
	}
	if !equalStrings(f.reporter.completed, []string{"commit"}) {
		t.Errorf("completed = %v, want [commit]", f.reporter.completed)
	}
}

func TestResolveCustomStepUsesRunCommand(t *testing.T) {
	config := &Config{
		Order: []string{"smoke"},
		Steps: map[string]*StepConfig{
			"smoke": {Run: CommandList{ShellCommand("./scripts/smoke.sh")}},
		},
	}

	steps := mustResolve(t, config, ResolveOptions{})
	if len(steps) != 1 {
		t.Fatalf("steps = %v, want one", stepNames(steps))
	}
	smoke := steps[0]

	if smoke.IsBuiltin {
		t.Error("custom step must not be builtin")
	}
	if got := singleShellAction(t, smoke); got != "./scripts/smoke.sh" {
		t.Errorf("smoke action = %q", got)
	}
}

func TestResolveErrorsWhenUnknownStepHasNoRun(t *testing.T) {
	_, err := Resolve(&Config{Order: []string{"bogus"}}, ResolveOptions{})

	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("err = %v, want mention of 'bogus'", err)
	}
}

func TestResolveOnlySkipsEverythingElse(t *testing.T) {
	steps := mustResolve(t, &Config{}, ResolveOptions{Only: []string{"commit"}})

	if !findStep(t, steps, "commit").Enabled() {
		t.Error("commit should be enabled")
	}
	if got := findStep(t, steps, "version").SkipReason; got != "not in --only" {
		t.Errorf("version skip reason = %q", got)
	}
	if got := findStep(t, steps, "push").SkipReason; got != "not in --only" {
		t.Errorf("push skip reason = %q", got)
	}
}

func TestResolveSkipDisablesNamedSteps(t *testing.T) {
	steps := mustResolve(t, &Config{}, ResolveOptions{Skip: []string{"push"}})

	if got := findStep(t, steps, "push").SkipReason; got != "--skip" {
		t.Errorf("push skip reason = %q, want --skip", got)
	}
}

func TestResolveFromSkipsEarlierSteps(t *testing.T) {
	steps := mustResolve(t, &Config{}, ResolveOptions{From: "publish"})

	if got := findStep(t, steps, "version").SkipReason; got != "before --from" {
		t.Errorf("version skip reason = %q", got)
	}
	if got := findStep(t, steps, "commit").SkipReason; got != "before --from" {
		t.Errorf("commit skip reason = %q", got)
	}
	if !findStep(t, steps, "publish").Enabled() {
		t.Error("publish should be enabled")
	}
	if !findStep(t, steps, "release").Enabled() {
		t.Error("release should be enabled")
	}
}

func TestResolveToSkipsLaterSteps(t *testing.T) {
	steps := mustResolve(t, &Config{}, ResolveOptions{To: "commit"})

	if !findStep(t, steps, "commit").Enabled() {
		t.Error("commit should be enabled")
	}
	if got := findStep(t, steps, "publish").SkipReason; got != "after --to" {
		t.Errorf("publish skip reason = %q", got)
	}
	if got := findStep(t, steps, "release").SkipReason; got != "after --to" {
		t.Errorf("release skip reason = %q", got)
	}
}

func TestResolveFromToKeepsOnlyTheRange(t *testing.T) {
	steps := mustResolve(t, &Config{}, ResolveOptions{From: "commit", To: "publish"})

	var enabled []string
	for _, step := range steps {
		if step.Enabled() {
			enabled = append(enabled, step.Name)
		}
	}
	if !equalStrings(enabled, []string{"commit", "build", "publish"}) {
		t.Errorf("enabled = %v, want [commit build publish]", enabled)
	}
}

func TestResolveFromUnknownStepErrors(t *testing.T) {
	_, err := Resolve(&Config{}, ResolveOptions{From: "bogus"})

	if err == nil || !strings.Contains(err.Error(), "--from") || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("err = %v, want mention of --from and bogus", err)
	}
}

func TestRunDelegatesBuiltinsToConfiguredTool(t *testing.T) {
	// Interop: the version/publish built-ins shell the configured tool
	// (e.g. the Node changeset CLI).
	f := newFixture(nil)
	config := &Config{Tool: "npx changeset", Order: []string{"version", "publish"}}

	f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	var shellCommands []string
	for _, call := range f.runner.calls {
		if call.shell {
			shellCommands = append(shellCommands, call.args[0])
		}
	}
	want := []string{"npx changeset version", "npx changeset publish --no-git-tag"}
	if !equalStrings(shellCommands, want) {
		t.Errorf("shell commands = %v, want %v", shellCommands, want)
	}
}

func TestResolveDisabledStepSetsSkipReason(t *testing.T) {
	config := &Config{
		Steps: map[string]*StepConfig{"push": {Enabled: boolPtr(false)}},
	}

	push := findStep(t, mustResolve(t, config, ResolveOptions{}), "push")
	if push.SkipReason != "disabled" {
		t.Errorf("push skip reason = %q, want disabled", push.SkipReason)
	}
}

func TestRunDryRunDoesNotExecuteAndReportsPlan(t *testing.T) {
	f := newFixture(nil)
	steps := mustResolve(t, &Config{}, ResolveOptions{})

	success := f.pipeline.Run(steps, &Config{}, true)

	if !success {
		t.Error("dry run should succeed")
	}
	if len(f.runner.calls) != 0 {
		t.Errorf("dry run executed %d commands", len(f.runner.calls))
	}
	if f.reporter.plannedDryRun == nil || !*f.reporter.plannedDryRun {
		t.Error("plan should be reported as a dry run")
	}
	if f.reporter.success == nil || !*f.reporter.success {
		t.Error("run should be reported successful")
	}
}

func TestRunRunsBeforeActionAfterInOrder(t *testing.T) {
	f := newFixture(nil)
	config := &Config{
		Order: []string{"commit"},
		Steps: map[string]*StepConfig{
			"commit": {
				Before:  CommandList{ShellCommand("echo before")},
				After:   CommandList{ShellCommand("echo after")},
				Message: strPtr("release v1"),
			},
		},
	}

	success := f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	if !success {
		t.Error("run should succeed")
	}
	want := []string{
		"sh -c echo before",
		"git add -A",
		"sh -c git diff --cached --quiet || git commit -m 'release v1'",
		"sh -c echo after",
	}
	if !equalStrings(f.runner.lines(), want) {
		t.Errorf("calls = %v, want %v", f.runner.lines(), want)
	}
	if !equalStrings(f.reporter.completed, []string{"commit"}) {
		t.Errorf("completed = %v, want [commit]", f.reporter.completed)
	}
}

func TestRunStopsOnFailureRunsOnErrorAndSkipsLaterSteps(t *testing.T) {
	f := newFixture(nil)
	config := &Config{
		Hooks: &Hooks{OnError: CommandList{ShellCommand("cleanup")}},
	}

	// Fail the `git commit` action; everything after it must not run. The commit
	// action is now a shell guard ("… || git commit …"), so match the command
	// line rather than an argv token.
	f.runner.responder = func(call recordedCommand) ([]string, int) {
		if strings.Contains(call.line(), "git commit") {
			return []string{"boom"}, 1
		}
		return nil, 0
	}

	success := f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	if success {
		t.Error("run should fail")
	}
	if f.reporter.success == nil || *f.reporter.success {
		t.Error("run should be reported failed")
	}

	lines := f.runner.lines()
	commitIndex, cleanupIndex := -1, -1
	for i, line := range lines {
		switch line {
		case "sh -c git diff --cached --quiet || git commit -m 'chore: release'":
			commitIndex = i
		case "sh -c cleanup":
			cleanupIndex = i
		}
	}
	if commitIndex < 0 || cleanupIndex < 0 || cleanupIndex < commitIndex {
		t.Errorf("calls = %v, want commit then cleanup in order", lines)
	}
	for _, call := range f.runner.calls {
		if call.hasArg("push") {
			t.Errorf("push ran after the failure: %v", lines)
		}
	}
}

func TestRunInterpolatesToolAndDispatchesShellThroughSh(t *testing.T) {
	f := newFixture(nil)
	config := &Config{
		Tool:  "mytool",
		Order: []string{"custom"},
		Steps: map[string]*StepConfig{
			"custom": {Run: CommandList{ShellCommand("${tool} do-thing")}},
		},
	}

	f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	if len(f.runner.calls) != 1 {
		t.Fatalf("calls = %v, want one", f.runner.lines())
	}
	call := f.runner.calls[0]
	if !call.shell {
		t.Error("custom shell command should dispatch through the shell")
	}
	if !equalStrings(call.args, []string{"mytool do-thing"}) {
		t.Errorf("args = %v, want [mytool do-thing]", call.args)
	}
}

func TestRunResolvesLazyVarOnReferenceInterpolatesAndMasks(t *testing.T) {
	f := newFixture(nil)
	config := &Config{
		Order: []string{"publish"},
		Steps: map[string]*StepConfig{
			"publish": {Args: []string{"--otp", "${vars.npmOtp}"}},
		},
		Vars: map[string]*VarSpec{
			"npmOtp": {Command: specPtr(ArgvCommand("op", "otp")), Lazy: true},
		},
	}

	// The capture command returns the OTP; everything else succeeds.
	f.runner.responder = func(call recordedCommand) ([]string, int) {
		if !call.shell && call.args[0] == "op" {
			return []string{"123456"}, 0
		}
		return nil, 0
	}

	success := f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	if !success {
		t.Error("run should succeed")
	}

	// The capture ran, and the OTP was interpolated into the publish command.
	captured, shellCalls := false, 0
	var shellCommand string
	for _, call := range f.runner.calls {
		if !call.shell && call.args[0] == "op" {
			captured = true
		}
		if call.shell {
			shellCalls++
			shellCommand = call.args[0]
		}
	}
	if !captured {
		t.Error("the op capture command never ran")
	}
	if shellCalls != 1 || shellCommand != "changeset publish --no-git-tag --otp 123456" {
		t.Errorf("shell calls = %d (%q), want one 'changeset publish --no-git-tag --otp 123456'", shellCalls, shellCommand)
	}

	// The captured value is registered as a secret to redact.
	if got := f.masker.Mask("token=123456"); got != "token=***" {
		t.Errorf("Mask = %q, want token=***", got)
	}
}

func TestRunLazyVarNotCapturedWhenNeverReferenced(t *testing.T) {
	f := newFixture(nil)
	config := &Config{
		Order: []string{"push"},
		Vars: map[string]*VarSpec{
			"npmOtp": {Command: specPtr(ArgvCommand("op", "otp")), Lazy: true},
		},
	}

	f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	for _, call := range f.runner.calls {
		if !call.shell && call.args[0] == "op" {
			t.Error("the lazy var was captured without being referenced")
		}
	}
}

func TestRunEagerVarCaptureFailureAbortsBeforeAnySteps(t *testing.T) {
	f := newFixture(nil)
	config := &Config{
		Order: []string{"version"},
		Vars: map[string]*VarSpec{
			"broken": {Command: specPtr(ArgvCommand("false")), Lazy: false},
		},
	}

	f.runner.responder = func(call recordedCommand) ([]string, int) {
		if !call.shell && call.args[0] == "false" {
			return []string{"nope"}, 1
		}
		return nil, 0
	}

	success := f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	if success {
		t.Error("run should fail")
	}
	if f.reporter.success == nil || *f.reporter.success {
		t.Error("run should be reported failed")
	}
	// The version step must not have run after the eager capture failed.
	if len(f.runner.calls) != 1 || f.runner.calls[0].line() != "false" {
		t.Errorf("calls = %v, want only the failed capture", f.runner.lines())
	}
}

func TestRunDryRunDoesNotCaptureVars(t *testing.T) {
	f := newFixture(nil)
	config := &Config{
		Vars: map[string]*VarSpec{
			"eager": {Command: specPtr(ArgvCommand("op", "otp")), Lazy: false},
		},
	}

	f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, true)

	if len(f.runner.calls) != 0 {
		t.Errorf("dry run executed %d commands", len(f.runner.calls))
	}
}

func TestResolveConfirmUsesDefaultOrCustomMessage(t *testing.T) {
	config := &Config{
		Order: []string{"publish", "push"},
		Steps: map[string]*StepConfig{
			"publish": {Confirm: ConfirmDefault()},
			"push":    {Confirm: ConfirmText("Push to origin?")},
		},
	}

	steps := mustResolve(t, config, ResolveOptions{})

	publish := findStep(t, steps, "publish")
	if publish.Confirm == nil || *publish.Confirm != DefaultConfirmMessage("publish") {
		t.Errorf("publish confirm = %v, want default message", publish.Confirm)
	}
	push := findStep(t, steps, "push")
	if push.Confirm == nil || *push.Confirm != "Push to origin?" {
		t.Errorf("push confirm = %v, want custom message", push.Confirm)
	}
}

func TestRunConfirmGateProceedsWhenApproved(t *testing.T) {
	f := newFixture(nil)
	f.prompter.answer = true
	config := &Config{
		Order: []string{"version"},
		Steps: map[string]*StepConfig{"version": {Confirm: ConfirmDefault()}},
	}

	success := f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	if !success {
		t.Error("run should succeed")
	}
	if !equalStrings(f.prompter.prompts, []string{DefaultConfirmMessage("version")}) {
		t.Errorf("prompts = %v, want the default version prompt", f.prompter.prompts)
	}
	if len(f.runner.calls) != 1 || !f.runner.calls[0].shell || f.runner.calls[0].args[0] != "changeset version" {
		t.Errorf("calls = %v, want one 'changeset version'", f.runner.lines())
	}
}

func TestRunConfirmGateDeclinedStopsBeforeActionWithoutFailing(t *testing.T) {
	f := newFixture(nil)
	f.prompter.answer = false
	config := &Config{
		Order: []string{"version", "push"},
		Hooks: &Hooks{OnError: CommandList{ShellCommand("cleanup")}},
		Steps: map[string]*StepConfig{
			"version": {
				Before:  CommandList{ShellCommand("echo prep")},
				Confirm: ConfirmDefault(),
			},
		},
	}

	success := f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	if success {
		t.Error("run should not succeed after a declined gate")
	}
	if f.reporter.cancelledStep != "version" {
		t.Errorf("cancelled step = %q, want version", f.reporter.cancelledStep)
	}

	// before-hooks run before the gate; the action and later steps do not;
	// onError is NOT a cancel path.
	if !equalStrings(f.runner.lines(), []string{"sh -c echo prep"}) {
		t.Errorf("calls = %v, want only the before hook", f.runner.lines())
	}
}

func TestRunNativeStepInvokesRegisteredHandler(t *testing.T) {
	invoked := false
	f := newFixture(map[string]NativeHandler{
		"release": func() bool { invoked = true; return true },
	})
	config := &Config{Order: []string{"release"}}

	success := f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	if !success {
		t.Error("run should succeed")
	}
	if !invoked {
		t.Error("the native handler was not invoked")
	}
	if !equalStrings(f.reporter.completed, []string{"release"}) {
		t.Errorf("completed = %v, want [release]", f.reporter.completed)
	}
}

func TestRunNativeStepHandlerFailureFailsRelease(t *testing.T) {
	f := newFixture(map[string]NativeHandler{
		"release": func() bool { return false },
	})
	config := &Config{Order: []string{"release"}}

	success := f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	if success {
		t.Error("run should fail")
	}
	if f.reporter.success == nil || *f.reporter.success {
		t.Error("run should be reported failed")
	}
}

func TestRunNativeStepNoHandlerSkipsGracefully(t *testing.T) {
	// The fixture has no native handlers registered.
	f := newFixture(nil)
	config := &Config{Order: []string{"release"}}

	success := f.pipeline.Run(mustResolve(t, config, ResolveOptions{}), config, false)

	if !success {
		t.Error("run should succeed")
	}
	if len(f.reporter.skippedSteps) != 1 || f.reporter.skippedSteps[0].name != "release" {
		t.Errorf("skipped = %v, want only release", f.reporter.skippedSteps)
	}
}

func TestResolveGithubReleaseOverriddenByRunBecomesCommandStep(t *testing.T) {
	config := &Config{
		Order: []string{"release"},
		Steps: map[string]*StepConfig{
			"release": {Run: CommandList{ShellCommand("./custom-release.sh")}},
		},
	}

	steps := mustResolve(t, config, ResolveOptions{})
	if len(steps) != 1 {
		t.Fatalf("steps = %v, want one", stepNames(steps))
	}
	step := steps[0]

	if step.Kind != StepKindCommands {
		t.Error("release with run should be a command step")
	}
	if got := singleShellAction(t, step); got != "./custom-release.sh" {
		t.Errorf("action = %q", got)
	}
}

func specPtr(spec CommandSpec) *CommandSpec { return &spec }

// ${env.NAME} resolves from the layered release environment map, and a missing
// var resolves to the empty string (consuming the placeholder).
func TestInterpolateEnvFromLayeredMap(t *testing.T) {
	env := map[string]string{"NPM_TOKEN": "from-dotenv"}

	if got := interpolate(nil, env, "publish --token ${env.NPM_TOKEN}"); got != "publish --token from-dotenv" {
		t.Errorf("env interpolation = %q, want the .env value substituted", got)
	}
	if got := interpolate(nil, env, "x=${env.ABSENT}"); got != "x=" {
		t.Errorf("missing env var = %q, want empty (placeholder consumed)", got)
	}
}

// NewExecRunner runs each command with the provided environment, so a token
// declared only in the layered .env reaches the spawned release command.
func TestNewExecRunnerPassesEnvToCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	runner := NewExecRunner([]string{"FROM_DOTENV=yes"})

	out, code, err := runner(true, []string{`printf %s "$FROM_DOTENV"`}, "")
	if err != nil || code != 0 {
		t.Fatalf("run failed: code=%d err=%v", code, err)
	}
	if len(out) != 1 || out[0] != "yes" {
		t.Errorf("command output = %v, want the env value [yes]", out)
	}
}
