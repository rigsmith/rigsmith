package cli

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/internal/shiprig/pipeline"
)

// signingConfig is a velopack app configured to sign, with env as its
// signing.env block.
func signingConfig(t *testing.T, env string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(`{ "velopack": { "signing": { "enabled": true, "env": ` + env + ` } } }`))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func resolveForTest(t *testing.T, cfg *config.Config, unsigned bool) (map[string]string, bool, string) {
	return resolveForTestDry(t, cfg, unsigned, false)
}

func resolveForTestDry(t *testing.T, cfg *config.Config, unsigned, dryBuild bool) (map[string]string, bool, string) {
	t.Helper()
	var lines []string
	out := func(l ...string) { lines = append(lines, l...) }
	env, ok := resolveSigningEnv(context.Background(), cfg, "velopack", pipeline.NewSecretMasker(), unsigned, dryBuild, "build app", out)
	return env, ok, strings.Join(lines, "\n")
}

// A release configured to sign used to warn and build UNSIGNED on a terminal
// when a secret couldn't be read. It fails now, wherever it runs, naming the
// secret and how to supply it — and never printing a value it did read.
func TestResolveSigningEnv_UnreadableSecretFailsWithItsNameAndTheFix(t *testing.T) {
	t.Setenv("RIGSMITH_TEST_SIGN_IDENTITY", "s3cret-identity-value")
	t.Setenv("RIGSMITH_TEST_SIGN_P12", "")
	cfg := signingConfig(t, `{
		"MACOS_SIGN_IDENTITY": "env:RIGSMITH_TEST_SIGN_IDENTITY",
		"MACOS_SIGN_P12": "env:RIGSMITH_TEST_SIGN_P12"
	}`)

	env, ok, output := resolveForTest(t, cfg, false)
	if ok || env != nil {
		t.Fatalf("an unreadable signing secret must fail the step, got ok=%v env=%v\n%s", ok, env, output)
	}
	for _, want := range []string{
		"MACOS_SIGN_P12",
		"not building unsigned",
		"set RIGSMITH_TEST_SIGN_P12 in the environment",
		"--unsigned",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "s3cret-identity-value") {
		t.Fatalf("a resolved secret's value reached the output:\n%s", output)
	}
	if strings.Contains(output, "proceeding UNSIGNED") {
		t.Fatalf("still degrading to an unsigned build:\n%s", output)
	}
}

// A cmd: helper's stderr can carry the secret it was meant to print. A failure
// names the reference by kind only and never repeats that output.
func TestResolveSigningEnv_HelperOutputNeverReachesTheMessage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cmd: references run through sh")
	}
	cfg := signingConfig(t, `{ "MACOS_SIGN_P12": "cmd:echo leaked-s3cret >&2; exit 1" }`)
	for _, dry := range []bool{false, true} {
		_, _, output := resolveForTestDry(t, cfg, false, dry)
		if strings.Contains(output, "leaked-s3cret") {
			t.Fatalf("dryBuild=%v: a helper's stderr reached the output:\n%s", dry, output)
		}
		if !strings.Contains(output, "MACOS_SIGN_P12") {
			t.Errorf("dryBuild=%v: the failing variable isn't named:\n%s", dry, output)
		}
	}
}

func TestResolveSigningEnv_DopplerSecretPointsAtDopplerLogin(t *testing.T) {
	// No secrets reader in the context, as when .rig.json has no doppler block
	// or doppler can't be reached: the reference can't resolve.
	cfg := signingConfig(t, `{ "MACOS_SIGN_P12": "doppler:MACOS_SIGN_P12" }`)
	_, ok, output := resolveForTest(t, cfg, false)
	if ok {
		t.Fatalf("an unreadable doppler secret must fail the step:\n%s", output)
	}
	for _, want := range []string{"MACOS_SIGN_P12", "doppler login", "--unsigned"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

// --unsigned is the deliberate way to build unsigned: nothing is read, the
// build proceeds, and the output says so.
func TestResolveSigningEnv_UnsignedBuildsUnsignedOnPurpose(t *testing.T) {
	t.Setenv("RIGSMITH_TEST_SIGN_P12", "")
	cfg := signingConfig(t, `{ "MACOS_SIGN_P12": "env:RIGSMITH_TEST_SIGN_P12" }`)
	env, ok, output := resolveForTest(t, cfg, true)
	if !ok || env != nil {
		t.Fatalf("--unsigned must proceed with no signing env, got ok=%v env=%v", ok, env)
	}
	if !strings.Contains(output, "building unsigned (--unsigned)") {
		t.Fatalf("an unsigned build must say it is one:\n%s", output)
	}
}

// A --dry-build packs an unsigned snapshot that ships nowhere: an unreadable
// secret is said, not fatal, and the message says a release would stop.
func TestResolveSigningEnv_DryBuildReportsAndCarriesOn(t *testing.T) {
	t.Setenv("RIGSMITH_TEST_SIGN_P12", "")
	cfg := signingConfig(t, `{ "MACOS_SIGN_P12": "env:RIGSMITH_TEST_SIGN_P12" }`)
	env, ok, output := resolveForTestDry(t, cfg, false, true)
	if !ok || env != nil {
		t.Fatalf("a dry-build must carry on unsigned, got ok=%v env=%v", ok, env)
	}
	for _, want := range []string{"MACOS_SIGN_P12", "a release would stop here"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestResolveSigningEnv_NotConfiguredIsQuiet(t *testing.T) {
	cfg, err := config.Parse([]byte(`{ "velopack": { "signing": { "enabled": false, "env": { "X": "env:NOPE" } } } }`))
	if err != nil {
		t.Fatal(err)
	}
	env, ok, output := resolveForTest(t, cfg, false)
	if !ok || env != nil || output != "" {
		t.Fatalf("signing off: ok=%v env=%v output=%q", ok, env, output)
	}
}
