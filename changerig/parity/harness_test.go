// Package parity runs the shared parity corpus (core/testdata/parity) through the
// real changerig binary and asserts that its version decisions and changelog
// output match the frozen Node @changesets goldens.
//
// This is the Go end of the cross-implementation parity check described in
// core/testdata/parity/README.md: net-changesets (C#) asserts the same scenarios
// against the same goldens, so a single corpus keeps both ports honest against
// upstream @changesets.
package parity

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var update = flag.Bool("parity.diff", false, "on a changelog mismatch, write the actual output next to the golden as CHANGELOG.actual.md for diffing")

// --- corpus model (mirrors scenarios.json) ---

type release struct {
	Package string `json:"package"`
	Bump    string `json:"bump"`
}

type changesetSpec struct {
	File     string    `json:"file"`
	Summary  string    `json:"summary"`
	Releases []release `json:"releases"`
}

// depRef is an in-repo dependency. In JSON it is either a bare package name
// (range defaults to that dependency's exact starting version, like the original
// fixtures) or {"name": "...", "range": "^1.0.0"} to pin a specific range.
type depRef struct {
	Name  string
	Range string
}

func (d *depRef) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &d.Name)
	}
	var obj struct {
		Name  string `json:"name"`
		Range string `json:"range"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	d.Name, d.Range = obj.Name, obj.Range
	return nil
}

type pkgSpec struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Dependencies []depRef `json:"dependencies"`
}

type scenario struct {
	ID                         string                       `json:"id"`
	UpdateInternalDependencies string                       `json:"updateInternalDependencies"`
	Fixed                      [][]string                   `json:"fixed"`
	Linked                     [][]string                   `json:"linked"`
	Ignore                     []string                     `json:"ignore"`
	Packages                   []pkgSpec                    `json:"packages"`
	Changesets                 []changesetSpec              `json:"changesets"`
	ExpectedVersions           map[string]string            `json:"expectedVersions"`
	ExpectedRanges             map[string]map[string]string `json:"expectedRanges"`
	// KnownDivergence marks packages whose changelog deliberately differs from
	// the Node golden (package → why). Versions and ranges must still agree;
	// TestParity skips the byte-compare and TestKnownDivergence asserts the
	// outputs really do differ (self-policing, like net-changesets'
	// KnownDivergenceTests).
	KnownDivergence map[string]string `json:"knownDivergence"`
	// NetDivergence marks packages where the net-changesets C# tool deliberately
	// differs from Node AND Go (package → why) — e.g. it does not cascade
	// dependents of group-pulled members. TestDotnetCrossOracle skips these.
	NetDivergence map[string]string `json:"netDivergence"`
}

type corpus struct {
	Scenarios []scenario `json:"scenarios"`
}

var (
	repoRoot     string
	corpusDir    string
	changerigBin string
)

func TestMain(m *testing.M) {
	flag.Parse()

	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "parity: locate repo root:", err)
		os.Exit(1)
	}
	repoRoot = root
	corpusDir = filepath.Join(root, "core", "testdata", "parity")

	bin := filepath.Join(os.TempDir(), "changerig-parity")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "./changerig")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "parity: build changerig:\n%s\n", out)
		os.Exit(1)
	}
	changerigBin = bin

	os.Exit(m.Run())
}

func TestParity(t *testing.T) {
	c := loadCorpus(t)
	for _, sc := range c.Scenarios {
		t.Run(sc.ID, func(t *testing.T) {
			dir := t.TempDir()
			writeNodeRepo(t, dir, sc)
			runVersion(t, dir)

			for _, p := range sc.Packages {
				// Oracle 1: the version decision.
				got := readNodeVersion(t, dir, p.Name)
				if want := sc.ExpectedVersions[p.Name]; got != want {
					t.Errorf("version(%s): got %q, want %q", p.Name, got, want)
				}

				// Oracle 2: the rendered changelog, byte-for-byte (normalized).
				// A package with no golden is one Node did not release (e.g. an
				// in-range dependent); assert no CHANGELOG was written for it.
				if _, divergent := sc.KnownDivergence[p.Name]; divergent {
					continue // asserted by TestKnownDivergence instead
				}
				goldenPath := filepath.Join(corpusDir, "golden", sc.ID, p.Name, "CHANGELOG.md")
				actualPath := filepath.Join(dir, "packages", p.Name, "CHANGELOG.md")
				if !fileExists(goldenPath) {
					if fileExists(actualPath) {
						t.Errorf("changelog(%s): package was not released by Node, but changerig wrote a CHANGELOG:\n%s",
							p.Name, readFile(t, actualPath))
					}
					continue
				}
				want := normalize(readFile(t, goldenPath))
				gotCL := normalize(readFile(t, actualPath))
				if gotCL != want {
					if *update {
						_ = os.WriteFile(
							filepath.Join(corpusDir, "golden", sc.ID, p.Name, "CHANGELOG.actual.md"),
							[]byte(gotCL+"\n"), 0o644)
					}
					t.Errorf("changelog(%s) diverges from the Node golden:\n%s",
						p.Name, diff(want, gotCL))
				}
			}

			// Oracle 3: the rewritten in-repo dependency ranges in each manifest
			// (e.g. ^1.0.0 → ^2.0.0, or left untouched for an unreleased dependent).
			for pkgName, wantDeps := range sc.ExpectedRanges {
				gotDeps := readNodeDeps(t, dir, pkgName)
				for dep, wantRange := range wantDeps {
					if gotDeps[dep] != wantRange {
						t.Errorf("range(%s→%s): got %q, want %q (Node)", pkgName, dep, gotDeps[dep], wantRange)
					}
				}
			}
		})
	}
}

// TestKnownDivergence documents the one place the Go port deliberately does not
// match Node @changesets, as a self-policing guard (ported from net-changesets'
// KnownDivergenceTests): on a TRANSITIVE dependency entry Node drops the
// "Updated dependencies" header and emits only the bare nested bullet; Go keeps
// the header, matching net-changesets. The golden stays the frozen Node output,
// and this test asserts Go's changelog still differs from it in exactly that
// way. If the outputs ever converge, this fails on purpose — remove the
// scenario's knownDivergence marker to promote it into the matching set.
func TestKnownDivergence(t *testing.T) {
	c := loadCorpus(t)
	ran := false
	for _, sc := range c.Scenarios {
		if len(sc.KnownDivergence) == 0 {
			continue
		}
		ran = true
		t.Run(sc.ID, func(t *testing.T) {
			dir := t.TempDir()
			writeNodeRepo(t, dir, sc)
			runVersion(t, dir)

			for pkg, why := range sc.KnownDivergence {
				golden := normalize(readFile(t, filepath.Join(corpusDir, "golden", sc.ID, pkg, "CHANGELOG.md")))
				got := normalize(readFile(t, filepath.Join(dir, "packages", pkg, "CHANGELOG.md")))
				if got == golden {
					t.Errorf("changelog(%s) now MATCHES the Node golden — the known divergence (%s) is gone; promote the scenario to the matching set", pkg, why)
				}
				// The divergence is purely the header: Go keeps it, Node's golden
				// has only the bare nested bullet.
				if !strings.Contains(got, "- Updated dependencies") {
					t.Errorf("changelog(%s): Go output lost the 'Updated dependencies' header:\n%s", pkg, got)
				}
				if strings.Contains(golden, "Updated dependencies") {
					t.Errorf("golden(%s) contains 'Updated dependencies' — it no longer captures the Node quirk:\n%s", pkg, golden)
				}
			}
		})
	}
	if !ran {
		t.Skip("no knownDivergence scenarios in the corpus")
	}
}

// TestStatusPlan checks the `status --output` JSON release plan (Oracle 1,
// independent of changelog formatting): it must list exactly the packages whose
// version changes, each with its expected new version. Run on a fresh
// materialization since `version` consumes the changesets.
func TestStatusPlan(t *testing.T) {
	c := loadCorpus(t)
	for _, sc := range c.Scenarios {
		t.Run(sc.ID, func(t *testing.T) {
			dir := t.TempDir()
			writeNodeRepo(t, dir, sc)

			planPath := filepath.Join(dir, "plan.json")
			cmd := exec.Command(changerigBin, "status", "--output", planPath)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("changerig status --output failed: %v\n%s", err, out)
			}

			var plan struct {
				Releases []struct {
					Name       string `json:"name"`
					Type       string `json:"type"`
					NewVersion string `json:"newVersion"`
				} `json:"releases"`
			}
			if err := json.Unmarshal([]byte(readFile(t, planPath)), &plan); err != nil {
				t.Fatalf("parse plan.json: %v", err)
			}

			startVersion := map[string]string{}
			for _, p := range sc.Packages {
				startVersion[p.Name] = p.Version
			}
			got := map[string]string{}
			for _, r := range plan.Releases {
				if r.Type == "" {
					t.Errorf("status release %s has empty type", r.Name)
				}
				// A "none" release (ignored or dev-dependent: ranges rewritten, no
				// version change) appears in Node's status plan too — accept it as
				// long as the version really is unchanged.
				if r.Type == "none" {
					if r.NewVersion != startVersion[r.Name] {
						t.Errorf("status plan: none release %s changes version %q → %q", r.Name, startVersion[r.Name], r.NewVersion)
					}
					continue
				}
				got[r.Name] = r.NewVersion
			}
			// Expected releases = packages whose version actually changes.
			want := map[string]string{}
			for _, p := range sc.Packages {
				if nv := sc.ExpectedVersions[p.Name]; nv != p.Version {
					want[p.Name] = nv
				}
			}
			for name, nv := range want {
				if got[name] != nv {
					t.Errorf("status plan: %s newVersion = %q, want %q", name, got[name], nv)
				}
			}
			for name := range got {
				if _, ok := want[name]; !ok {
					t.Errorf("status plan lists %s, but it should not be released", name)
				}
			}
		})
	}
}

// TestPrereleaseParity drives the full prerelease lifecycle through the changerig
// binary and checks it against Node @changesets goldens at each step:
//
//	pre enter next → version  (1.1.0-next.0)
//	+changeset, version       (1.1.0-next.1, counter advances, sections accumulate)
//	pre exit → version        (1.1.0 stable, consolidated section atop the pre history)
func TestPrereleaseParity(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{ "name": "root", "private": true, "workspaces": ["packages/*"] }`)
	writeFile(t, filepath.Join(dir, "package-lock.json"), "{}")
	mkdirAll(t, filepath.Join(dir, "packages", "pkg-a"))
	writeFile(t, filepath.Join(dir, "packages", "pkg-a", "package.json"), `{ "name": "pkg-a", "version": "1.0.0" }`)
	mkdirAll(t, filepath.Join(dir, ".changeset"))
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{ "updateInternalDependencies": "patch" }`)

	changelogPath := filepath.Join(dir, "packages", "pkg-a", "CHANGELOG.md")
	preDir := filepath.Join(corpusDir, "prerelease")

	step := func(name, wantVersion, goldenStep string) {
		t.Helper()
		runVersion(t, dir)
		if got := readNodeVersion(t, dir, "pkg-a"); got != wantVersion {
			t.Errorf("%s: pkg-a version = %q, want %q", name, got, wantVersion)
		}
		want := normalize(readFile(t, filepath.Join(preDir, goldenStep, "pkg-a", "CHANGELOG.md")))
		got := normalize(readFile(t, changelogPath))
		if got != want {
			t.Errorf("%s: changelog diverges from Node golden:\n%s", name, diff(want, got))
		}
	}

	writeFile(t, filepath.Join(dir, ".changeset", "c1.md"), "---\n\"pkg-a\": minor\n---\n\nFeature one")
	runChangerig(t, dir, "pre", "enter", "next")
	step("enter+version", "1.1.0-next.0", "step1")

	writeFile(t, filepath.Join(dir, ".changeset", "c2.md"), "---\n\"pkg-a\": patch\n---\n\nFix two")
	step("+patch+version", "1.1.0-next.1", "step2")

	runChangerig(t, dir, "pre", "exit")
	step("exit+version", "1.1.0", "step3")
}

// TestSnapshotParity checks `version --snapshot <tag>` against live-Node-verified
// behavior (@changesets v3.0.0-next.5). The version embeds a timestamp, so this
// asserts shape (regex) and a templated changelog instead of a frozen golden:
//
//   - every releasing package gets 0.0.0-<tag>-<14-digit datetime>;
//   - an out-of-range dependent's manifest range is pinned to the EXACT snapshot
//     version (operator dropped) and its changelog dep line carries it;
//   - an in-range dependent is untouched (release decisions follow stable math);
//   - changesets are consumed, like a normal version run.
func TestSnapshotParity(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{ "name": "root", "private": true, "workspaces": ["packages/*"] }`)
	writeFile(t, filepath.Join(dir, "package-lock.json"), "{}")
	for name, body := range map[string]string{
		"pkg-a": `{ "name": "pkg-a", "version": "1.0.0" }`,
		"pkg-b": `{ "name": "pkg-b", "version": "1.0.0", "dependencies": { "pkg-a": "1.0.0" } }`,
		"pkg-c": `{ "name": "pkg-c", "version": "1.0.0", "dependencies": { "pkg-a": "^1.0.0" } }`,
	} {
		mkdirAll(t, filepath.Join(dir, "packages", name))
		writeFile(t, filepath.Join(dir, "packages", name, "package.json"), body)
	}
	mkdirAll(t, filepath.Join(dir, ".changeset"))
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{ "updateInternalDependencies": "patch" }`)
	writeFile(t, filepath.Join(dir, ".changeset", "c.md"), "---\n\"pkg-a\": minor\n---\n\nSnapshot feature")

	runChangerig(t, dir, "version", "--snapshot", "canary")

	snapRe := regexp.MustCompile(`^0\.0\.0-canary-\d{14}$`)
	gotA := readNodeVersion(t, dir, "pkg-a")
	if !snapRe.MatchString(gotA) {
		t.Fatalf("pkg-a version = %q, want 0.0.0-canary-<datetime>", gotA)
	}
	if gotB := readNodeVersion(t, dir, "pkg-b"); gotB != gotA {
		t.Errorf("pkg-b version = %q, want the same snapshot version as pkg-a (%q)", gotB, gotA)
	}
	// pkg-b's manifest range is pinned to the exact snapshot version.
	if deps := readNodeDeps(t, dir, "pkg-b"); deps["pkg-a"] != gotA {
		t.Errorf("pkg-b range(pkg-a) = %q, want exact pin %q (Node drops the operator)", deps["pkg-a"], gotA)
	}
	// pkg-c (in-range ^1.0.0 against the stable 1.1.0) is untouched.
	if gotC := readNodeVersion(t, dir, "pkg-c"); gotC != "1.0.0" {
		t.Errorf("pkg-c version = %q, want 1.0.0 (in-range dependent is never released)", gotC)
	}
	if deps := readNodeDeps(t, dir, "pkg-c"); deps["pkg-a"] != "^1.0.0" {
		t.Errorf("pkg-c range(pkg-a) = %q, want untouched ^1.0.0", deps["pkg-a"])
	}
	if fileExists(filepath.Join(dir, "packages", "pkg-c", "CHANGELOG.md")) {
		t.Error("pkg-c should have no CHANGELOG")
	}
	// Changesets are consumed (Node deletes them on a snapshot run too).
	if fileExists(filepath.Join(dir, ".changeset", "c.md")) {
		t.Error("snapshot version should consume the changeset (Node-verified)")
	}

	wantA := normalize(strings.ReplaceAll(`# pkg-a

## {V}
### Minor Changes

- Snapshot feature
`, "{V}", gotA))
	if got := normalize(readFile(t, filepath.Join(dir, "packages", "pkg-a", "CHANGELOG.md"))); got != wantA {
		t.Errorf("pkg-a snapshot changelog diverges:\n%s", diff(wantA, got))
	}
	wantB := normalize(strings.ReplaceAll(`# pkg-b

## {V}
### Patch Changes

- Updated dependencies
  - pkg-a@{V}
`, "{V}", gotA))
	if got := normalize(readFile(t, filepath.Join(dir, "packages", "pkg-b", "CHANGELOG.md"))); got != wantB {
		t.Errorf("pkg-b snapshot changelog diverges:\n%s", diff(wantB, got))
	}
}

// --- fixture writing (mirrors net-changesets ParityFixtures.WriteNodeRepo) ---

func writeNodeRepo(t *testing.T, root string, sc scenario) {
	t.Helper()
	mkdirAll(t, filepath.Join(root, ".changeset"))

	writeFile(t, filepath.Join(root, "package.json"),
		`{ "name": "root", "private": true, "workspaces": ["packages/*"] }`)
	// A lockfile lets workspace detection settle on npm.
	writeFile(t, filepath.Join(root, "package-lock.json"), "{}")

	versionOf := map[string]string{}
	for _, p := range sc.Packages {
		versionOf[p.Name] = p.Version
	}

	for _, p := range sc.Packages {
		dir := filepath.Join(root, "packages", p.Name)
		mkdirAll(t, dir)
		deps := ""
		if len(p.Dependencies) > 0 {
			parts := make([]string, 0, len(p.Dependencies))
			for _, d := range p.Dependencies {
				rng := d.Range
				if rng == "" {
					rng = versionOf[d.Name] // default: exact starting version
				}
				parts = append(parts, fmt.Sprintf("%q: %q", d.Name, rng))
			}
			deps = `, "dependencies": { ` + strings.Join(parts, ", ") + ` }`
		}
		writeFile(t, filepath.Join(dir, "package.json"),
			fmt.Sprintf(`{ "name": %q, "version": %q%s }`, p.Name, p.Version, deps))
	}

	cfg := map[string]any{"updateInternalDependencies": sc.UpdateInternalDependencies}
	if len(sc.Fixed) > 0 {
		cfg["fixed"] = sc.Fixed
	}
	if len(sc.Linked) > 0 {
		cfg["linked"] = sc.Linked
	}
	if len(sc.Ignore) > 0 {
		cfg["ignore"] = sc.Ignore
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".changeset", "config.json"), string(cfgJSON))

	for _, cs := range sc.Changesets {
		var fm strings.Builder
		for _, r := range cs.Releases {
			fmt.Fprintf(&fm, "%q: %s\n", r.Package, r.Bump)
		}
		writeFile(t, filepath.Join(root, ".changeset", cs.File),
			fmt.Sprintf("---\n%s---\n\n%s", fm.String(), cs.Summary))
	}
}

func runVersion(t *testing.T, dir string) {
	t.Helper()
	runChangerig(t, dir, "version")
}

func runChangerig(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(changerigBin, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("changerig %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// --- readback ---

func readNodeVersion(t *testing.T, root, pkg string) string {
	t.Helper()
	var pj struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(root, "packages", pkg, "package.json"))), &pj); err != nil {
		t.Fatalf("parse %s package.json: %v", pkg, err)
	}
	return pj.Version
}

func readNodeDeps(t *testing.T, root, pkg string) map[string]string {
	t.Helper()
	var pj struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(root, "packages", pkg, "package.json"))), &pj); err != nil {
		t.Fatalf("parse %s package.json: %v", pkg, err)
	}
	return pj.Dependencies
}

// --- helpers ---

func loadCorpus(t *testing.T) corpus {
	t.Helper()
	var c corpus
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(corpusDir, "scenarios.json"))), &c); err != nil {
		t.Fatalf("parse scenarios.json: %v", err)
	}
	if len(c.Scenarios) == 0 {
		t.Fatal("no scenarios loaded")
	}
	return c
}

// normalize trims trailing whitespace per line and trailing blank lines, matching
// net-changesets ParityFixtures.Normalize.
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// diff renders an aligned line-by-line want/got so a single blank-line drift is obvious.
func diff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	n := max(len(w), len(g))
	var b strings.Builder
	fmt.Fprintf(&b, "%-40s | %s\n", "want (node golden)", "got (changerig)")
	for i := 0; i < n; i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		mark := "  "
		if wl != gl {
			mark = "≠ "
		}
		fmt.Fprintf(&b, "%s%-38s | %s\n", mark, wl, gl)
	}
	return b.String()
}

func findRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.work not found above %s", filepath.Dir(file))
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
