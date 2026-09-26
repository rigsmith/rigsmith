package scripts

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// scripts/npm/trust-publishers.mjs moves every wrapper package's npm trusted
// publisher, and a mistake there is silent until a release cannot publish. Two
// were found by hand before these tests existed: `npm trust list --json` prints
// one object per configuration, one after another, and reading that as a single
// JSON value made every package with two "unreadable"; and matching a
// configuration by file name alone let another repository's release.yml count
// as ours, so --replace revoked this repository's only publisher and
// registered nothing.
//
// These drive the real script against a fake npm that keeps registry state in
// files and prints `trust list` the way npm does.

const fakeNpm = `#!/usr/bin/env node
const fs = require('fs'), path = require('path'), crypto = require('crypto')
const state = process.env.FAKE_NPM_STATE
const a = process.argv.slice(2)
const file = (n) => path.join(state, n.replace('/', '__') + '.json')
const load = (n) => fs.existsSync(file(n)) ? JSON.parse(fs.readFileSync(file(n), 'utf8')) : []
const save = (n, v) => fs.writeFileSync(file(n), JSON.stringify(v))
if (a[0] === '--version') { console.log('11.17.0'); process.exit(0) }
if (a[0] === 'whoami') { console.log('tester'); process.exit(0) }
if (process.env.FAKE_NPM_EOTP) {
  console.log(JSON.stringify({ error: { code: 'EOTP', summary: 'This operation requires a one-time password.' } }))
  process.exit(1)
}
const opt = (k) => a[a.indexOf(k) + 1]
if (a[0] === 'trust' && a[1] === 'list') {
  for (const c of load(a[2])) console.log(JSON.stringify(c, null, 2) + '\n')
  process.exit(0)
}
if (a[0] === 'trust' && a[1] === 'github') {
  const cs = load(a[2])
  if (cs.some((c) => c.file === opt('--file') && c.repository === opt('--repo'))) {
    console.error('npm error this trust relationship already exists'); process.exit(1)
  }
  cs.push({ id: crypto.randomUUID(), type: 'github', file: opt('--file'), repository: opt('--repo') })
  save(a[2], cs); process.exit(0)
}
if (a[0] === 'trust' && a[1] === 'revoke') {
  save(a[2], load(a[2]).filter((c) => c.id !== opt('--id'))); process.exit(0)
}
console.error('fake npm: unexpected ' + a.join(' ')); process.exit(2)
`

type trustConfig struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	File       string `json:"file"`
	Repository string `json:"repository"`
}

type trustFixture struct {
	t        *testing.T
	bin      string // holds the fake npm
	state    string
	dist     string
	packages []string
}

func newTrustFixture(t *testing.T) *trustFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake npm is a shebang script")
	}
	if _, err := exec.LookPath("node"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node isn't on PATH on CI; these tests would never run where they matter")
		}
		t.Skip("node isn't on PATH")
	}
	f := &trustFixture{t: t, bin: t.TempDir(), state: t.TempDir(), dist: t.TempDir()}
	if err := os.WriteFile(filepath.Join(f.bin, "npm"), []byte(fakeNpm), 0o755); err != nil {
		t.Fatal(err)
	}
	// One package per tool in cmd/, as build-packages.mjs lays them out: the
	// script refuses a build that's missing a tool.
	tools, err := os.ReadDir("../cmd")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if !tool.IsDir() {
			continue
		}
		name := "@rigsmith/" + tool.Name()
		dir := filepath.Join(f.dist, tool.Name())
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest, _ := json.Marshal(map[string]string{"name": name, "version": "1.0.0"})
		if err := os.WriteFile(filepath.Join(dir, "package.json"), manifest, 0o644); err != nil {
			t.Fatal(err)
		}
		f.packages = append(f.packages, name)
	}
	sort.Strings(f.packages)
	if len(f.packages) < 5 {
		t.Fatalf("only %d tools in cmd/; the seeds below need five", len(f.packages))
	}
	return f
}

func (f *trustFixture) seed(name string, configs ...trustConfig) {
	for i := range configs {
		configs[i].ID = fmt.Sprintf("%s-%d", strings.ReplaceAll(name, "/", "_"), i)
		configs[i].Type = "github"
	}
	raw, _ := json.Marshal(configs)
	if err := os.WriteFile(filepath.Join(f.state, strings.Replace(name, "/", "__", 1)+".json"), raw, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *trustFixture) held(name string) []trustConfig {
	raw, err := os.ReadFile(filepath.Join(f.state, strings.Replace(name, "/", "__", 1)+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	var cs []trustConfig
	if err := json.Unmarshal(raw, &cs); err != nil {
		f.t.Fatal(err)
	}
	return cs
}

func (f *trustFixture) run(extraEnv []string, args ...string) (string, int) {
	cmd := exec.Command("node", append([]string{"npm/trust-publishers.mjs"}, args...)...)
	cmd.Env = append(os.Environ(),
		"PATH="+f.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_NPM_STATE="+f.state,
		"TRUST_PUBLISHERS_DIST="+f.dist,
		"TRUST_SPACING_MS=0",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	code := 0
	if e, ok := err.(*exec.ExitError); ok {
		code = e.ExitCode()
	} else if err != nil {
		f.t.Fatalf("running the script: %v\n%s", err, out)
	}
	return string(out), code
}

const (
	thisRepo  = "rigsmith/rigsmith"
	otherRepo = "someone/else"
)

// seedEveryCase gives the packages, in turn: both workflows, only the old one,
// only the new one, the old one beside ANOTHER repository's release.yml, and a
// prerelease.yml (which ends with release.yml but isn't it).
func (f *trustFixture) seedEveryCase() {
	cases := [][]trustConfig{
		{{File: "goreleaser.yml", Repository: thisRepo}, {File: "release.yml", Repository: thisRepo}},
		{{File: "goreleaser.yml", Repository: thisRepo}},
		{{File: "release.yml", Repository: thisRepo}},
		{{File: "goreleaser.yml", Repository: thisRepo}, {File: "release.yml", Repository: otherRepo}},
		{{File: "prerelease.yml", Repository: thisRepo}},
	}
	for i, name := range f.packages {
		f.seed(name, cases[i%len(cases)]...)
	}
}

func TestTrustReplaceLeavesOnlyThisRepositorysReleaseWorkflow(t *testing.T) {
	f := newTrustFixture(t)
	f.seedEveryCase()

	out, code := f.run(nil, "--replace", "--otp", "000000")
	if code != 0 {
		t.Fatalf("--replace exited %d:\n%s", code, out)
	}
	for i, name := range f.packages {
		var ours, others []string
		for _, c := range f.held(name) {
			if c.Repository == thisRepo {
				ours = append(ours, c.File)
			} else {
				others = append(others, c.Repository+" "+c.File)
			}
		}
		if len(ours) != 1 || ours[0] != "release.yml" {
			t.Errorf("%s (seed %d): this repository's configurations = %v, want exactly [release.yml]", name, i%5, ours)
		}
		// Another repository's configuration is never touched.
		if i%5 == 3 && (len(others) != 1 || others[0] != otherRepo+" release.yml") {
			t.Errorf("%s: another repository's configurations = %v, want it kept", name, others)
		}
	}
}

func TestTrustListReadsSeveralConfigurationsAndFailsUntilExclusive(t *testing.T) {
	f := newTrustFixture(t)
	f.seedEveryCase()

	out, code := f.run(nil, "--list", "--otp", "000000")
	if strings.Contains(out, "could not be read") {
		t.Fatalf("--list couldn't read packages with several configurations:\n%s", out)
	}
	if code == 0 {
		t.Errorf("--list exited 0 with stale and foreign configurations present:\n%s", out)
	}
	for _, want := range []string{"--replace drops it", "ALSO trusted by another repository", "DIFFERENT workflow"} {
		if !strings.Contains(out, want) {
			t.Errorf("--list output lacks %q:\n%s", want, out)
		}
	}

	// Exclusive everywhere: it passes.
	for _, name := range f.packages {
		f.seed(name, trustConfig{File: "release.yml", Repository: thisRepo})
	}
	if out, code := f.run(nil, "--list", "--otp", "000000"); code != 0 {
		t.Errorf("--list exited %d with every package on release.yml alone:\n%s", code, out)
	}
}

func TestTrustListNamesAOneTimePasswordFailure(t *testing.T) {
	f := newTrustFixture(t)
	out, code := f.run([]string{"FAKE_NPM_EOTP=1"}, "--list", "--otp", "000000")
	if code == 0 {
		t.Errorf("--list exited 0 when every read failed:\n%s", out)
	}
	if !strings.Contains(out, "EOTP") || !strings.Contains(out, "one-time password") {
		t.Errorf("--list doesn't say the one-time password is the problem:\n%s", out)
	}
}
