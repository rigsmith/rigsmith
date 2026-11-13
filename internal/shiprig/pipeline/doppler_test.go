package pipeline

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/secrets"
)

// feedKey is a value no output may ever contain.
const feedKey = "feedz-push-value-never-printed"

// fakeDoppler is a signed-in provider holding one release secret.
type fakeDoppler struct {
	values map[string]string
	reads  []string
}

func (f *fakeDoppler) Name() string                        { return "doppler" }
func (f *fakeDoppler) Title() string                       { return "Doppler" }
func (f *fakeDoppler) Ready(context.Context, string) error { return nil }
func (f *fakeDoppler) Wrap(_ secrets.Scope, argv []string) []string {
	return argv
}
func (f *fakeDoppler) Get(_ context.Context, _ string, scope secrets.Scope, name string) (string, error) {
	f.reads = append(f.reads, scope.String()+"/"+name)
	v, ok := f.values[name]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (f *fakeDoppler) All(context.Context, string, secrets.Scope) (map[string]string, error) {
	return f.values, nil
}

func notInCI(t *testing.T) {
	t.Helper()
	prev := secrets.InCI
	secrets.InCI = func() bool { return false }
	t.Cleanup(func() { secrets.InCI = prev })
}

// The feed-push var as a repo mid-move writes it: Doppler's release config
// first, the keychain item it used before as the fallback.
func feedConfig() *Config {
	ref := "doppler:FEEDZ_API_KEY"
	return &Config{
		Order: []string{"push"},
		Vars: map[string]*VarSpec{"key": {
			Secret:  &ref,
			Command: ptr(ArgvCommand("security", "find-generic-password", "-s", "feedz-push-jcamp", "-w")),
			Lazy:    true,
		}},
		Steps: map[string]*StepConfig{"push": {Run: CommandList{ShellCommand("dotnet nuget push x.nupkg --api-key ${vars.key}")}}},
	}
}

func runFeedPush(t *testing.T, settings secrets.Settings, doppler *fakeDoppler, runner *recordingRunner) (string, bool) {
	t.Helper()
	ctx := context.Background()
	if doppler != nil {
		reader := secrets.NewReader(&secrets.Setup{Provider: doppler, Settings: settings}, secrets.Release, "/tmp/repo")
		ctx = secrets.WithReader(ctx, reader)
	}
	var out bytes.Buffer
	masker := NewSecretMasker()
	p := New(runner.run, NewPlainReporter(&out, masker, "shiprig"), masker, &stubPrompter{answer: true}, "/tmp/repo", nil, nil, nil).WithContext(ctx)
	config := feedConfig()
	if err := validateVars(config.Vars); err != nil {
		t.Fatalf("the doppler-with-fallback form must validate: %v", err)
	}
	ok := p.Run(mustResolve(t, config, ResolveOptions{}), config, false)
	return out.String(), ok
}

func TestDopplerVarReadsTheReleaseConfigAndNamesIt(t *testing.T) {
	notInCI(t)
	doppler := &fakeDoppler{values: map[string]string{"FEEDZ_API_KEY": feedKey}}
	runner := &recordingRunner{}
	out, ok := runFeedPush(t, secrets.Settings{Project: "owlwatch", Release: "release"}, doppler, runner)
	if !ok {
		t.Fatalf("run failed:\n%s", out)
	}
	if strings.Join(doppler.reads, ",") != "owlwatch/release/FEEDZ_API_KEY" {
		t.Errorf("reads = %v, want the release config", doppler.reads)
	}
	for _, c := range runner.calls {
		if c.hasArg("security") {
			t.Error("the keychain fallback ran although Doppler is configured")
		}
	}
	if !strings.Contains(strings.Join(runner.lines(), "\n"), "--api-key "+feedKey) {
		t.Errorf("the key was not passed to the push: %v", runner.lines())
	}
	if !strings.Contains(out, "vars.key ← Doppler owlwatch/release (FEEDZ_API_KEY)") {
		t.Errorf("the output does not name the source:\n%s", out)
	}
	if strings.Contains(out, feedKey) {
		t.Errorf("the value reached the output:\n%s", out)
	}
}

func TestDopplerVarFallsBackToTheKeychainWithoutAReleaseConfig(t *testing.T) {
	notInCI(t)
	runner := &recordingRunner{responder: func(c recordedCommand) ([]string, int) {
		if c.hasArg("feedz-push-jcamp") {
			return []string{feedKey}, 0
		}
		return nil, 0
	}}
	doppler := &fakeDoppler{}
	out, ok := runFeedPush(t, secrets.Settings{Project: "owlwatch", Dev: "dev"}, doppler, runner)
	if !ok {
		t.Fatalf("run failed:\n%s", out)
	}
	if len(doppler.reads) != 0 {
		t.Errorf("Doppler was read with no release config: %v", doppler.reads)
	}
	if !strings.Contains(out, "vars.key ← capture command (fallback: no doppler.release in .rig.json)") {
		t.Errorf("the output does not say the fallback was used:\n%s", out)
	}
	if strings.Contains(out, feedKey) {
		t.Errorf("the value reached the output:\n%s", out)
	}
}

func TestDopplerVarWithNoDopplerBlockFallsBack(t *testing.T) {
	notInCI(t)
	runner := &recordingRunner{responder: func(recordedCommand) ([]string, int) { return []string{feedKey}, 0 }}
	out, ok := runFeedPush(t, secrets.Settings{}, nil, runner)
	if !ok || !strings.Contains(out, "fallback") {
		t.Fatalf("a repo with no doppler block keeps its keychain:\n%s", out)
	}
}

func TestConfiguredDopplerNeverFallsBackOnAMissingSecret(t *testing.T) {
	notInCI(t)
	runner := &recordingRunner{responder: func(recordedCommand) ([]string, int) { return []string{feedKey}, 0 }}
	out, ok := runFeedPush(t, secrets.Settings{Project: "owlwatch", Release: "release"}, &fakeDoppler{}, runner)
	if ok {
		t.Fatalf("a secret missing from the release config must fail the run:\n%s", out)
	}
	for _, c := range runner.calls {
		if c.hasArg("security") {
			t.Error("the keychain ran after Doppler, which is configured, came back empty")
		}
	}
}

func TestOnlyAManagerRefMayCarryAFallback(t *testing.T) {
	ref := "env:FEEDZ_API_KEY"
	err := validateVars(map[string]*VarSpec{"key": {Secret: &ref, Command: ptr(ShellCommand("security -w"))}})
	if err == nil || !strings.Contains(err.Error(), "doppler:NAME") {
		t.Fatalf("validateVars = %v, want the pairing refused for env:", err)
	}
}
