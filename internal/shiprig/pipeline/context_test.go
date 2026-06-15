package pipeline

import (
	"strings"
	"testing"
)

type fakeRelCtx struct {
	pkgs   []ReleasePackage
	urls   map[string]string
	issues []IssueRef
}

func (f fakeRelCtx) Packages() []ReleasePackage   { return f.pkgs }
func (f fakeRelCtx) ReleaseURL(key string) string { return f.urls[key] }
func (f fakeRelCtx) Issues() []IssueRef           { return f.issues }

func twoPackages() fakeRelCtx {
	return fakeRelCtx{
		pkgs: []ReleasePackage{
			{Name: "@acme/web", Key: "web", Ecosystem: "node", Version: "2.1.0", Tag: "@acme/web@2.1.0", Changelog: "web notes"},
			{Name: "acme/cli", Key: "cli", Ecosystem: "go", Version: "1.4.0", Tag: "cli/v1.4.0", Changelog: "cli notes"},
		},
		urls: map[string]string{"web": "https://forge/web", "cli": "https://forge/cli"},
	}
}

// mustResolveVar asserts the key is a release variable and returns its value.
func mustResolveVar(t *testing.T, rv *releaseVars, key string) string {
	t.Helper()
	v, isRel, err := rv.resolve(key)
	if !isRel {
		t.Fatalf("%q was not recognised as a release variable", key)
	}
	if err != nil {
		t.Fatalf("resolve(%q) error: %v", key, err)
	}
	return v
}

func TestReleaseVarAddressedAndAlias(t *testing.T) {
	rv := newReleaseVars(twoPackages())

	if got := mustResolveVar(t, rv, "version.web"); got != "2.1.0" {
		t.Errorf("version.web = %q", got)
	}
	if got := mustResolveVar(t, rv, "tag.cli"); got != "cli/v1.4.0" {
		t.Errorf("tag.cli = %q", got)
	}
	// The full manifest name works as an exact alias.
	if got := mustResolveVar(t, rv, "version.@acme/web"); got != "2.1.0" {
		t.Errorf("version.@acme/web (alias) = %q", got)
	}
}

func TestReleaseVarAggregatesSortedByName(t *testing.T) {
	rv := newReleaseVars(twoPackages())

	if got := mustResolveVar(t, rv, "versions"); got != "@acme/web@2.1.0, acme/cli@1.4.0" {
		t.Errorf("versions = %q", got)
	}
	if got := mustResolveVar(t, rv, "tags"); got != "@acme/web@2.1.0, cli/v1.4.0" {
		t.Errorf("tags = %q", got)
	}
}

func TestReleaseVarBareSinglePackage(t *testing.T) {
	rv := newReleaseVars(fakeRelCtx{pkgs: []ReleasePackage{
		{Name: "@acme/web", Key: "web", Version: "2.1.0", Tag: "@acme/web@2.1.0"},
	}})

	if got := mustResolveVar(t, rv, "version"); got != "2.1.0" {
		t.Errorf("bare version (single package) = %q", got)
	}
}

func TestReleaseVarBareMultiPackageIsAmbiguous(t *testing.T) {
	rv := newReleaseVars(twoPackages())

	_, isRel, err := rv.resolve("version")
	if !isRel {
		t.Fatal("version should be a release variable")
	}
	if err == nil {
		t.Fatal("bare ${version} in a multi-package release should error")
	}
	for _, want := range []string{"ambiguous", "@acme/web", "acme/cli", "${version.<package>}", "${versions}"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestReleaseVarChangelogBareSuggestsAddressedOnly(t *testing.T) {
	_, _, err := newReleaseVars(twoPackages()).resolve("changelog")
	if err == nil {
		t.Fatal("bare ${changelog} in a multi-package release should error")
	}
	if !strings.Contains(err.Error(), "${changelog.<package>}") {
		t.Errorf("changelog error should suggest the addressed form: %q", err)
	}
	if strings.Contains(err.Error(), "for all") {
		t.Errorf("changelog has no aggregate form, should not suggest one: %q", err)
	}
}

func TestReleaseVarUnknownAddressIsError(t *testing.T) {
	_, _, err := newReleaseVars(twoPackages()).resolve("version.nope")
	if err == nil || !strings.Contains(err.Error(), "unknown package") {
		t.Errorf("err = %v, want unknown-package error", err)
	}
}

func TestReleaseVarURLsAggregateSkipsEmpty(t *testing.T) {
	ctx := twoPackages()
	ctx.urls = map[string]string{"web": "https://forge/web"} // cli URL not yet known
	rv := newReleaseVars(ctx)

	if got := mustResolveVar(t, rv, "releaseUrls"); got != "https://forge/web" {
		t.Errorf("releaseUrls = %q, want only the known URL", got)
	}
	if got := mustResolveVar(t, rv, "releaseUrl.web"); got != "https://forge/web" {
		t.Errorf("releaseUrl.web = %q", got)
	}
}

func TestReleaseVarIssuesAndIssueBranch(t *testing.T) {
	rv := newReleaseVars(fakeRelCtx{issues: []IssueRef{
		{Number: 57, Branch: "issue-57"}, {Number: 42, Branch: "issue-42"}, {Number: 42, Branch: "issue-42"},
	}})

	if got := mustResolveVar(t, rv, "issues"); got != "#42, #57" {
		t.Errorf("issues = %q, want sorted+deduped '#42, #57'", got)
	}
	if got := mustResolveVar(t, rv, "issueBranch.42"); got != "issue-42" {
		t.Errorf("issueBranch.42 = %q", got)
	}
	if _, _, err := rv.resolve("issueBranch"); err == nil {
		t.Error("bare ${issueBranch} with multiple issues should be ambiguous")
	}
}

func TestReleaseVarIssueBranchZeroIssuesIsEmptyNotError(t *testing.T) {
	v, isRel, err := newReleaseVars(fakeRelCtx{}).resolve("issueBranch")
	if !isRel || err != nil {
		t.Fatalf("issueBranch with no issues: isRel=%v err=%v", isRel, err)
	}
	if v != "" {
		t.Errorf("issueBranch with no issues = %q, want empty", v)
	}
}

func TestReleaseVarNonReleaseKeyFallsThrough(t *testing.T) {
	rv := newReleaseVars(twoPackages())
	for _, key := range []string{"tool", "vars.npmOtp", "env.TOKEN", "unknown"} {
		if _, isRel, _ := rv.resolve(key); isRel {
			t.Errorf("%q should not be treated as a release variable", key)
		}
	}
}

func TestNewReleaseVarsNilContext(t *testing.T) {
	if newReleaseVars(nil) != nil {
		t.Error("newReleaseVars(nil) must be nil so the engine skips release interpolation")
	}
}

func TestRunInterpolatesReleaseAggregate(t *testing.T) {
	runner := &recordingRunner{}
	reporter := &recordingReporter{}
	p := New(runner.run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", nil, nil, twoPackages())

	config := &Config{
		Order: []string{"notify"},
		Steps: map[string]*StepConfig{
			"notify": {Run: CommandList{ShellCommand("echo ${versions}")}},
		},
	}
	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Fatal("run should succeed")
	}
	if got := strings.Join(runner.lines(), "\n"); !strings.Contains(got, "echo @acme/web@2.1.0, acme/cli@1.4.0") {
		t.Errorf("command not interpolated: %q", got)
	}
}

func TestRunBareVersionAmbiguousFailsRunWithoutExecuting(t *testing.T) {
	runner := &recordingRunner{}
	reporter := &recordingReporter{}
	p := New(runner.run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", nil, nil, twoPackages())

	config := &Config{
		Order: []string{"notify"},
		Steps: map[string]*StepConfig{
			"notify": {Run: CommandList{ShellCommand("echo ${version}")}},
		},
	}
	if p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Error("ambiguous bare ${version} should fail the run")
	}
	if reporter.success == nil || *reporter.success {
		t.Error("run should be reported as failed")
	}
	if len(runner.calls) != 0 {
		t.Errorf("the command must not run when interpolation fails; calls=%v", runner.lines())
	}
}
