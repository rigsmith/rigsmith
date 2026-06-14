package parity

// The dotnet half of the corpus: the same language-neutral scenarios
// materialized as a csproj tree (mirroring net-changesets'
// ParityFixtures.WriteNetRepo) instead of an npm workspace. Two tests:
//
//   - TestDotnetParity   — changerig version on the csproj tree must reproduce
//     the SAME frozen Node goldens (versions + changelogs). One corpus, every
//     ecosystem.
//   - TestDotnetCrossOracle — runs the real net-changesets C# CLI and changerig
//     on identical fixtures and diffs their outputs byte-for-byte. Skipped when
//     the C# oracle is not built locally (like the C# suite skips without Node).
//
// Scenarios whose dependencies carry an explicit npm range (in-range-*) are
// npm-only — csproj ProjectReferences are rangeless — and are excluded here, as
// is Oracle 3 (manifest range rewrites, meaningless for csproj).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dotnetApplicable reports whether a scenario is expressible as a csproj tree:
// every in-repo dependency must be rangeless (a bare ProjectReference).
func dotnetApplicable(sc scenario) bool {
	for _, p := range sc.Packages {
		for _, d := range p.Dependencies {
			if d.Range != "" {
				return false
			}
		}
	}
	return true
}

// writeDotnetRepo mirrors net-changesets ParityFixtures.WriteNetRepo:
// src/<name>/<name>.csproj with an inline <Version> and rangeless
// ProjectReferences; the shared .changeset/config.json carries the same keys
// the node materialization writes (plus dotnet.sourcePath, which the C# tool
// needs and the Go tool ignores).
func writeDotnetRepo(t *testing.T, root string, sc scenario) {
	t.Helper()
	mkdirAll(t, filepath.Join(root, ".changeset"))

	for _, p := range sc.Packages {
		dir := filepath.Join(root, "src", p.Name)
		mkdirAll(t, dir)
		var refs strings.Builder
		if len(p.Dependencies) > 0 {
			refs.WriteString("  <ItemGroup>\n")
			for _, d := range p.Dependencies {
				fmt.Fprintf(&refs, "    <ProjectReference Include=\"..\\%s\\%s.csproj\" />\n", d.Name, d.Name)
			}
			refs.WriteString("  </ItemGroup>\n")
		}
		writeFile(t, filepath.Join(dir, p.Name+".csproj"), fmt.Sprintf(
			"<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <TargetFramework>net8.0</TargetFramework>\n    <Version>%s</Version>\n  </PropertyGroup>\n%s</Project>\n",
			p.Version, refs.String()))
	}

	cfg := fmt.Sprintf(`{ "updateInternalDependencies": %q, "dotnet": { "sourcePath": "src" }`, sc.UpdateInternalDependencies)
	if len(sc.Fixed) > 0 {
		cfg += `, "fixed": ` + jsonGroups(sc.Fixed)
	}
	if len(sc.Linked) > 0 {
		cfg += `, "linked": ` + jsonGroups(sc.Linked)
	}
	if len(sc.Ignore) > 0 {
		cfg += `, "ignore": ["` + strings.Join(sc.Ignore, `", "`) + `"]`
	}
	cfg += " }"
	writeFile(t, filepath.Join(root, ".changeset", "config.json"), cfg)

	for _, cs := range sc.Changesets {
		var fm strings.Builder
		for _, r := range cs.Releases {
			fmt.Fprintf(&fm, "%q: %s\n", r.Package, r.Bump)
		}
		writeFile(t, filepath.Join(root, ".changeset", cs.File),
			fmt.Sprintf("---\n%s---\n\n%s", fm.String(), cs.Summary))
	}
}

func jsonGroups(groups [][]string) string {
	parts := make([]string, len(groups))
	for i, g := range groups {
		parts[i] = `["` + strings.Join(g, `", "`) + `"]`
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

var csprojVersionRe = regexp.MustCompile(`<Version>([^<]*)</Version>`)

func readDotnetVersion(t *testing.T, root, pkg string) string {
	t.Helper()
	data := readFile(t, filepath.Join(root, "src", pkg, pkg+".csproj"))
	m := csprojVersionRe.FindStringSubmatch(data)
	if m == nil {
		t.Fatalf("no <Version> in %s.csproj", pkg)
	}
	return m[1]
}

// TestDotnetParity asserts changerig reproduces the Node goldens when the same
// scenarios are materialized as a csproj tree — proving the engine's decisions
// and changelog output are ecosystem-independent.
func TestDotnetParity(t *testing.T) {
	c := loadCorpus(t)
	for _, sc := range c.Scenarios {
		if !dotnetApplicable(sc) {
			continue
		}
		t.Run(sc.ID, func(t *testing.T) {
			dir := t.TempDir()
			writeDotnetRepo(t, dir, sc)
			runVersion(t, dir)

			for _, p := range sc.Packages {
				if got, want := readDotnetVersion(t, dir, p.Name), sc.ExpectedVersions[p.Name]; got != want {
					t.Errorf("version(%s): got %q, want %q", p.Name, got, want)
				}

				if _, divergent := sc.KnownDivergence[p.Name]; divergent {
					continue // asserted (against the node tree) by TestKnownDivergence
				}
				goldenPath := filepath.Join(corpusDir, "golden", sc.ID, p.Name, "CHANGELOG.md")
				actualPath := filepath.Join(dir, "src", p.Name, "CHANGELOG.md")
				if !fileExists(goldenPath) {
					if fileExists(actualPath) {
						t.Errorf("changelog(%s): not released by Node, but changerig wrote a CHANGELOG:\n%s",
							p.Name, readFile(t, actualPath))
					}
					continue
				}
				want := normalize(readFile(t, goldenPath))
				got := normalize(readFile(t, actualPath))
				if got != want {
					t.Errorf("changelog(%s) diverges from the Node golden on a dotnet tree:\n%s",
						p.Name, diff(want, got))
				}
			}
		})
	}
}

// netChangesetsCli locates the built net-changesets CLI dll, preferring
// $CHANGERIG_NET_DLL, then the conventional build outputs in ~/Git/net-changesets.
// Empty when unavailable (the cross-oracle test skips).
func netChangesetsCli() string {
	if p := os.Getenv("CHANGERIG_NET_DLL"); p != "" {
		if fileExists(p) {
			return p
		}
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, cfg := range []string{"Release", "Debug"} {
		for _, tfm := range []string{"net10.0", "net8.0"} {
			p := filepath.Join(home, "Git", "net-changesets", "src", "Changesets", "bin", cfg, tfm, "Changesets.dll")
			if fileExists(p) {
				return p
			}
		}
	}
	return ""
}

// TestDotnetCrossOracle runs the real net-changesets C# CLI and changerig on
// identical csproj fixtures and requires byte-identical results (versions and
// normalized changelogs) — the "one corpus, both implementations" check, with
// the C# tool as a second live oracle next to the frozen Node goldens.
func TestDotnetCrossOracle(t *testing.T) {
	cli := netChangesetsCli()
	if cli == "" {
		t.Skip("net-changesets CLI not built (set CHANGERIG_NET_DLL or build ~/Git/net-changesets)")
	}
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK not on PATH")
	}

	c := loadCorpus(t)
	for _, sc := range c.Scenarios {
		if !dotnetApplicable(sc) {
			continue
		}
		t.Run(sc.ID, func(t *testing.T) {
			netDir, goDir := t.TempDir(), t.TempDir()
			writeDotnetRepo(t, netDir, sc)
			writeDotnetRepo(t, goDir, sc)

			cmd := exec.Command("dotnet", cli, "version")
			cmd.Dir = netDir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("net-changesets version failed: %v\n%s", err, out)
			}
			runVersion(t, goDir)

			for _, p := range sc.Packages {
				if why, divergent := sc.NetDivergence[p.Name]; divergent {
					t.Logf("skipping %s: known net divergence (%s)", p.Name, why)
					continue
				}
				netV := readDotnetVersion(t, netDir, p.Name)
				goV := readDotnetVersion(t, goDir, p.Name)
				if netV != goV {
					t.Errorf("version(%s): net-changesets %q, changerig %q", p.Name, netV, goV)
				}

				netCL := filepath.Join(netDir, "src", p.Name, "CHANGELOG.md")
				goCL := filepath.Join(goDir, "src", p.Name, "CHANGELOG.md")
				if fileExists(netCL) != fileExists(goCL) {
					t.Errorf("changelog(%s): net wrote=%v, go wrote=%v", p.Name, fileExists(netCL), fileExists(goCL))
					continue
				}
				if !fileExists(netCL) {
					continue
				}
				want := normalize(readFile(t, netCL))
				got := normalize(readFile(t, goCL))
				if got != want {
					t.Errorf("changelog(%s): changerig diverges from net-changesets:\n%s",
						p.Name, diff(want, got))
				}
			}
		})
	}
}
