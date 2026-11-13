package secrets

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// planted is a value no message may ever contain.
const planted = "s3cr3t-value-never-printed"

func writeRigJSON(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".rig.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadReadsTheDopplerBlock(t *testing.T) {
	dir := writeRigJSON(t, `{
		// comments and trailing commas, as in any .rig.json
		"Doppler": { "project": "foxcraft", "dev": "dev", "ci": "ci", "release": "release", },
	}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || s.Provider.Name() != "doppler" {
		t.Fatalf("Load = %+v, want the doppler provider", s)
	}
	for c, want := range map[Context]string{Dev: "foxcraft/dev", CI: "foxcraft/ci", Release: "foxcraft/release"} {
		scope, ok := s.Scope(c)
		if !ok || scope.String() != want {
			t.Errorf("Scope(%s) = %q, %v; want %q", c, scope, ok, want)
		}
	}
}

func TestLoadWithoutABlockIsNoProvider(t *testing.T) {
	for name, body := range map[string]string{
		"no file":  "",
		"no block": `{ "quiet": true }`,
		"null":     `{ "doppler": null }`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if body != "" {
				dir = writeRigJSON(t, body)
			}
			s, err := Load(dir)
			if err != nil || s != nil {
				t.Fatalf("Load = %+v, %v; want nil, nil", s, err)
			}
		})
	}
}

func TestUnsetContextHasNoScope(t *testing.T) {
	dir := writeRigJSON(t, `{ "doppler": { "project": "foxcraft", "ci": "ci" } }`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Scope(Dev); ok {
		t.Error("dev has no config, so it must have no scope")
	}
	if _, ok := s.Scope(Release); ok {
		t.Error("release has no config, so it must have no scope")
	}
}

func TestLoadRejectsBadBlocks(t *testing.T) {
	for name, body := range map[string]string{
		"no project":         `{ "doppler": { "dev": "dev" } }`,
		"flag-shaped config": `{ "doppler": { "project": "foxcraft", "dev": "--debug" } }`,
		"space in project":   `{ "doppler": { "project": "fox craft", "dev": "dev" } }`,
		"not an object":      `{ "doppler": "foxcraft" }`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeRigJSON(t, body)); err == nil {
				t.Fatal("Load accepted a block it should refuse")
			}
		})
	}
}

func TestSelectRefusesTwoProviders(t *testing.T) {
	restore := SetProviderForTest("other", &fakeProvider{})
	defer restore()
	_, err := Select(map[string]*Settings{
		"doppler": {Project: "a", Dev: "dev"},
		"other":   {Project: "b", Dev: "dev"},
	})
	if err == nil || !strings.Contains(err.Error(), "keep one") {
		t.Fatalf("Select = %v, want a 'keep one' error", err)
	}
}

// fakeCLI is the exec seam: a scripted doppler.
type fakeCLI struct {
	installed bool
	token     string // what `configure get token` prints
	env       string // DOPPLER_TOKEN
	stdout    string
	stderr    string
	fail      bool
	calls     [][]string
}

func (f *fakeCLI) provider() *Doppler {
	return &Doppler{
		LookPath: func(string) (string, error) {
			if !f.installed {
				return "", exec.ErrNotFound
			}
			return "/usr/local/bin/doppler", nil
		},
		Getenv: func(k string) string {
			if k == "DOPPLER_TOKEN" {
				return f.env
			}
			return ""
		},
		Run: func(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
			f.calls = append(f.calls, args)
			if args[0] == "configure" {
				return []byte(f.token), nil, nil
			}
			if f.fail {
				return []byte(f.stdout), []byte(f.stderr), errors.New("exit status 1")
			}
			return []byte(f.stdout), []byte(f.stderr), nil
		},
	}
}

func TestReadyRefusesAMissingCLI(t *testing.T) {
	err := (&fakeCLI{}).provider().Ready(context.Background(), t.TempDir())
	if !errors.Is(err, ErrNotInstalled) || !strings.Contains(err.Error(), "doppler login") {
		t.Fatalf("Ready = %v, want ErrNotInstalled naming the fix", err)
	}
}

func TestReadyRefusesASignedOutCLI(t *testing.T) {
	err := (&fakeCLI{installed: true}).provider().Ready(context.Background(), t.TempDir())
	if !errors.Is(err, ErrSignedOut) || !strings.Contains(err.Error(), "`doppler login`") {
		t.Fatalf("Ready = %v, want ErrSignedOut naming `doppler login`", err)
	}
}

func TestReadyAcceptsALoginOrAToken(t *testing.T) {
	for name, f := range map[string]*fakeCLI{
		"login": {installed: true, token: planted + "\n"},
		"token": {installed: true, env: planted},
	} {
		t.Run(name, func(t *testing.T) {
			if err := f.provider().Ready(context.Background(), t.TempDir()); err != nil {
				t.Fatalf("Ready = %v", err)
			}
		})
	}
}

func TestWrapNamesProjectAndConfig(t *testing.T) {
	got := strings.Join(NewDoppler().Wrap(Scope{"foxcraft", "dev"}, []string{"go", "test", "./..."}), " ")
	if want := "doppler run -p foxcraft -c dev -- go test ./..."; got != want {
		t.Fatalf("Wrap = %q, want %q", got, want)
	}
}

func TestGetNamesTheScopeOnTheCall(t *testing.T) {
	f := &fakeCLI{installed: true, stdout: planted + "\n"}
	v, err := f.provider().Get(context.Background(), "/repo", Scope{"owlwatch", "release"}, "FEEDZ_API_KEY")
	if err != nil || v != planted {
		t.Fatalf("Get = %q, %v", v, err)
	}
	got := strings.Join(f.calls[0], " ")
	if !strings.Contains(got, "-p owlwatch -c release") || !strings.Contains(got, "secrets get FEEDZ_API_KEY --plain") {
		t.Fatalf("call = %q, want the project and config named", got)
	}
}

func TestGetFailuresCarryNoValue(t *testing.T) {
	scope := Scope{"owlwatch", "release"}
	cases := map[string]struct {
		f    *fakeCLI
		want error
	}{
		"not found": {&fakeCLI{installed: true, fail: true, stdout: planted, stderr: "Doppler Error: Could not find requested secret: KEY"}, ErrNotFound},
		"empty":     {&fakeCLI{installed: true, stdout: "  \n"}, ErrEmpty},
		"other":     {&fakeCLI{installed: true, fail: true, stdout: planted, stderr: "Doppler Error: Invalid Auth token"}, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := c.f.provider().Get(context.Background(), "/repo", scope, "KEY")
			if err == nil {
				t.Fatal("Get succeeded")
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("Get = %v, want %v", err, c.want)
			}
			if strings.Contains(err.Error(), planted) {
				t.Errorf("the error carries the value: %v", err)
			}
		})
	}
}

func TestAllNeverEchoesUnparseableOutput(t *testing.T) {
	f := &fakeCLI{installed: true, stdout: `{"KEY": "` + planted + `"` /* truncated JSON */}
	_, err := f.provider().All(context.Background(), "/repo", Scope{"foxcraft", "dev"})
	if err == nil || strings.Contains(err.Error(), planted) {
		t.Fatalf("All = %v; want an error without the value", err)
	}
}

func TestAllReadsTheJSON(t *testing.T) {
	f := &fakeCLI{installed: true, stdout: `{"PG_URL": "` + planted + `", "DOPPLER_CONFIG": "dev"}`}
	all, err := f.provider().All(context.Background(), "/repo", Scope{"foxcraft", "dev"})
	if err != nil || all["PG_URL"] != planted {
		t.Fatalf("All = %v, %v", all, err)
	}
	if got := strings.Join(f.calls[0], " "); !strings.Contains(got, "--no-file") {
		t.Fatalf("call = %q; a download must never write a file", got)
	}
}

func TestParseRef(t *testing.T) {
	for ref, want := range map[string]string{
		"doppler:FEEDZ_API_KEY":  "doppler FEEDZ_API_KEY",
		"Doppler:KEY":            "doppler KEY",
		"op://vault/item/field":  "",
		"env:FEEDZ_API_KEY":      "",
		"cmd:security find -w":   "",
		"infisical:KEY":          "", // not a provider yet
		"doppler://proj/cfg/KEY": "",
	} {
		p, n, ok := ParseRef(ref)
		got := ""
		if ok {
			got = p + " " + n
		}
		if got != want {
			t.Errorf("ParseRef(%q) = %q, want %q", ref, got, want)
		}
	}
}

func pinCI(t *testing.T, ci bool) {
	t.Helper()
	prev := InCI
	InCI = func() bool { return ci }
	t.Cleanup(func() { InCI = prev })
}

func TestReaderInCIReadsTheStepEnvironment(t *testing.T) {
	pinCI(t, true)
	s := &Setup{Provider: (&fakeCLI{}).provider(), Settings: Settings{Project: "owlwatch", CI: "ci"}}
	r := NewReader(s, Release, "/repo")
	r.getenv = func(k string) string {
		if k == "FEEDZ_API_KEY" {
			return planted
		}
		return ""
	}
	v, source, err := r.Get(context.Background(), "FEEDZ_API_KEY")
	if err != nil || v != planted || strings.Contains(source, planted) {
		t.Fatalf("Get = %q, %q, %v", v, source, err)
	}
	_, _, err = r.Get(context.Background(), "NOTARY_PASSWORD")
	if err == nil || !strings.Contains(err.Error(), "steps.doppler.outputs.NOTARY_PASSWORD") {
		t.Fatalf("a missing CI secret must fail and say how to pass it: %v", err)
	}
}

func TestReaderLocallyNeedsTheReleaseConfig(t *testing.T) {
	pinCI(t, false)
	f := &fakeCLI{installed: true, token: "t", stdout: planted}
	without := NewReader(&Setup{Provider: f.provider(), Settings: Settings{Project: "owlwatch", Dev: "dev"}}, Release, "/repo")
	if without.Available() {
		t.Fatal("no release config: the Reader must report unavailable, so a fallback runs")
	}
	if _, _, err := without.Get(context.Background(), "KEY"); err == nil || !strings.Contains(err.Error(), "doppler.release") {
		t.Fatalf("Get = %v, want the missing doppler.release named", err)
	}
	with := NewReader(&Setup{Provider: f.provider(), Settings: Settings{Project: "owlwatch", Release: "release"}}, Release, "/repo")
	v, source, err := with.Get(context.Background(), "KEY")
	if err != nil || v != planted || source != "Doppler owlwatch/release" {
		t.Fatalf("Get = %q, %q, %v", v, source, err)
	}
}

func TestReaderRefusesWhenSignedOut(t *testing.T) {
	pinCI(t, false)
	r := NewReader(&Setup{Provider: (&fakeCLI{installed: true}).provider(), Settings: Settings{Project: "owlwatch", Release: "release"}}, Release, "/repo")
	if _, _, err := r.Get(context.Background(), "KEY"); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("Get = %v, want ErrSignedOut", err)
	}
}

// workflowStep is the slice of a GitHub Actions step the generated YAML is
// checked against.
type workflowStep struct {
	Name  string            `yaml:"name"`
	ID    string            `yaml:"id"`
	Uses  string            `yaml:"uses"`
	Shell string            `yaml:"shell"`
	With  map[string]string `yaml:"with"`
	Env   map[string]string `yaml:"env"`
	Run   string            `yaml:"run"`
}

func generateSteps(t *testing.T, names ...string) []workflowStep {
	t.Helper()
	text, err := NewDoppler().CISteps(CIOptions{Scope: Scope{"foxcraft", "ci"}, Names: names})
	if err != nil {
		t.Fatal(err)
	}
	var steps []workflowStep
	if err := yaml.Unmarshal([]byte(text), &steps); err != nil {
		t.Fatalf("the generated steps are not valid YAML: %v\n%s", err, text)
	}
	return steps
}

func TestCIStepsPinTheFetchAndInjectNothing(t *testing.T) {
	steps := generateSteps(t, "POSTGRES_URL", "REDIS_URL")
	if len(steps) != 3 {
		t.Fatalf("got %d steps, want token check, fetch, guard", len(steps))
	}
	fetch := steps[1]
	if fetch.ID != "doppler" {
		t.Errorf("fetch id = %q", fetch.ID)
	}
	if want := "dopplerhq/secrets-fetch-action@451892f16195f9ac360e1a5bcbf0b5fd0e957534"; fetch.Uses != want {
		t.Errorf("uses = %q, want the commit pin %q", fetch.Uses, want)
	}
	if _, set := fetch.With["inject-env-vars"]; set {
		t.Error("the fetch must not inject env vars: every step would see every secret")
	}
	if fetch.With["doppler-token"] != "${{ secrets.DOPPLER_TOKEN }}" {
		t.Errorf("doppler-token = %q", fetch.With["doppler-token"])
	}
	if fetch.With["doppler-project"] != "foxcraft" || fetch.With["doppler-config"] != "ci" {
		t.Errorf("with = %v, want the project and config named", fetch.With)
	}
}

func TestCIStepsFailLoudlyOnAMissingTokenOrSecret(t *testing.T) {
	steps := generateSteps(t, "POSTGRES_URL", "REDIS_URL")
	check, guard := steps[0], steps[2]
	if check.Env["DOPPLER_TOKEN"] != "${{ secrets.DOPPLER_TOKEN }}" || !strings.Contains(check.Run, `-z "$DOPPLER_TOKEN"`) || !strings.Contains(check.Run, "exit 1") {
		t.Errorf("the token check does not fail on an unset token:\n%+v", check)
	}
	for _, n := range []string{"POSTGRES_URL", "REDIS_URL"} {
		if guard.Env["HAS_"+n] != "${{ steps.doppler.outputs."+n+" != '' }}" {
			t.Errorf("guard env HAS_%s = %q", n, guard.Env["HAS_"+n])
		}
		if !strings.Contains(guard.Run, `[ "$HAS_`+n+`" = true ] || missing="$missing `+n+`"`) {
			t.Errorf("guard does not check %s:\n%s", n, guard.Run)
		}
	}
	if !strings.Contains(guard.Run, `"$DOPPLER_SCOPE" != "foxcraft/ci"`) {
		t.Errorf("guard does not check the token reads foxcraft/ci:\n%s", guard.Run)
	}
	for _, s := range []workflowStep{check, guard} {
		if s.Shell != "bash" {
			t.Errorf("%q runs in %q; Windows runners default to pwsh", s.Name, s.Shell)
		}
	}
	// The guard learns whether each secret is set, never its value.
	for k, v := range guard.Env {
		if k != "DOPPLER_SCOPE" && !strings.HasSuffix(v, " != '' }}") {
			t.Errorf("guard env %s = %q passes a value to the step", k, v)
		}
	}
}

func TestCIStepsRefuseUnsafeInput(t *testing.T) {
	d := NewDoppler()
	for name, o := range map[string]CIOptions{
		"no names":           {Scope: Scope{"foxcraft", "ci"}},
		"expression in name": {Scope: Scope{"foxcraft", "ci"}, Names: []string{"A }}${{ github.token"}},
		"flag as config":     {Scope: Scope{"foxcraft", "-x"}, Names: []string{"A"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := d.CISteps(o); err == nil {
				t.Fatal("CISteps accepted it")
			}
		})
	}
}

func TestCIEnvTakesEachSecretFromTheStepOutputs(t *testing.T) {
	var step struct {
		Env map[string]string `yaml:"env"`
	}
	if err := yaml.Unmarshal([]byte(CIEnv("", []string{"PG_URL"}, 8)), &step); err != nil {
		t.Fatal(err)
	}
	if step.Env["PG_URL"] != "${{ steps.doppler.outputs.PG_URL }}" {
		t.Fatalf("env = %v", step.Env)
	}
}

// fakeProvider is a second provider for Select's two-blocks check.
type fakeProvider struct{}

func (fakeProvider) Name() string                                               { return "other" }
func (fakeProvider) Title() string                                              { return "Other" }
func (fakeProvider) Ready(context.Context, string) error                        { return nil }
func (fakeProvider) Wrap(_ Scope, argv []string) []string                       { return argv }
func (fakeProvider) Get(context.Context, string, Scope, string) (string, error) { return "", nil }
func (fakeProvider) All(context.Context, string, Scope) (map[string]string, error) {
	return nil, nil
}

func TestLoadRejectsAnUnknownKeyInTheBlock(t *testing.T) {
	_, err := Load(writeRigJSON(t, `{ "doppler": { "project": "owlwatch", "releasee": "release" } }`))
	if err == nil || !strings.Contains(err.Error(), "doppler.releasee") {
		t.Fatalf("Load = %v; a typo'd key must be refused, not read as no release config", err)
	}
}

func TestLoadRejectsTheBlockTwice(t *testing.T) {
	_, err := Load(writeRigJSON(t, `{ "doppler": { "project": "a", "release": "r" }, "Doppler": { "project": "b", "release": "r" } }`))
	if err == nil || !strings.Contains(err.Error(), "keep one") {
		t.Fatalf("Load = %v; two spellings of the block must be refused", err)
	}
}

func TestLoadOfAMalformedFile(t *testing.T) {
	if _, err := Load(writeRigJSON(t, `{ "doppler": { "project": "a" `)); err == nil {
		t.Error("a broken file naming doppler must be refused")
	}
	if s, err := Load(writeRigJSON(t, `{ "quiet": true `)); s != nil || err != nil {
		t.Errorf("a broken file naming no provider = %v, %v; want nil, nil (rig warns about it)", s, err)
	}
}

func TestLoadLayeredFallsBackToTheGlobalFile(t *testing.T) {
	global := filepath.Join(writeRigJSON(t, `{ "doppler": { "project": "owlwatch", "release": "release" } }`), ".rig.json")
	s, err := LoadLayered(t.TempDir(), global)
	if err != nil || s == nil || s.Settings.Project != "owlwatch" {
		t.Fatalf("LoadLayered = %+v, %v; want the global block", s, err)
	}
	repo := writeRigJSON(t, `{ "doppler": { "project": "foxcraft", "release": "release" } }`)
	if s, _ := LoadLayered(repo, global); s.Settings.Project != "foxcraft" {
		t.Fatalf("the repo's block must win, got %q", s.Settings.Project)
	}
}

func TestReaderInCIPassesTheValueOnAsReceived(t *testing.T) {
	pinCI(t, true)
	r := NewReader(&Setup{Provider: (&fakeCLI{}).provider(), Settings: Settings{Project: "p", CI: "ci"}}, Release, "/repo")
	r.getenv = func(string) string { return "  " + planted + "  " }
	if v, _, err := r.Get(context.Background(), "KEY"); err != nil || v != "  "+planted+"  " {
		t.Fatalf("Get = %q, %v; the value must not be trimmed", v, err)
	}
}

func TestIndentIsNormalizedAndNeverPanics(t *testing.T) {
	if NormalizeIndent(0) != DefaultIndent || NormalizeIndent(-3) != DefaultIndent || NormalizeIndent(4) != 4 {
		t.Fatal("NormalizeIndent")
	}
	if got := CIEnv("", []string{"A"}, -3); !strings.HasPrefix(got, "env:") {
		t.Fatalf("CIEnv with a negative indent = %q", got)
	}
}

// encoding/json keeps only the last of two identical keys, so the repeat has
// to be caught as written — otherwise the later block silently decides the
// credentials.
func TestLoadRejectsTheSameBlockKeyTwice(t *testing.T) {
	_, err := Load(writeRigJSON(t, `{
		"doppler": { "project": "a", "release": "release" },
		// a later, forgotten copy
		"doppler": { "project": "b", "release": "release" },
	}`))
	if err == nil || !strings.Contains(err.Error(), "keep one") {
		t.Fatalf("Load = %v; a repeated block key must be refused", err)
	}
}

// A field repeated inside the block would let the last copy pick the config —
// another context's secrets — so it is refused, case variants included.
func TestLoadRejectsAFieldRepeatedInTheBlock(t *testing.T) {
	for name, body := range map[string]string{
		"same spelling": `{ "doppler": { "project": "a", "dev": "dev", "dev": "prod" } }`,
		"case variant":  `{ "doppler": { "project": "a", "dev": "dev", "Dev": "prod" } }`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeRigJSON(t, body))
			if err == nil || !strings.Contains(err.Error(), "doppler.dev is set more than once") {
				t.Fatalf("Load = %v; a repeated field must be refused", err)
			}
		})
	}
}
