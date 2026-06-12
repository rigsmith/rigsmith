// Test-class resolution for `rig test <name>` in a .NET repo, ported from the
// .NET rig's TestVerb.FilterForName + TestEnumeration. The C# tool enumerates
// test classes from the BUILT assembly via MetadataLoadContext; Go has no CLR,
// so candidate class names come from a best-effort scan of the test project's
// *.cs sources instead (namespace + class declarations, classified with the
// same markers as TestEnumeration.IsTestClass). Matching uses rig's tiered
// fuzzy matcher (exact > prefix > substring > subsequence, as in `rig cd`);
// filter building is pure and mirrors the C# FullyQualifiedName shapes.
package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// runDotnetTest runs `dotnet [watch] test` on the repo's test project with the
// filter resolved from args[0] (a `~ = !~ !=` shorthand or a test-class query);
// the remaining args are forwarded. The .NET rig's TestVerb.Execute.
func runDotnetTest(cmd *cobra.Command, root string, args []string, watch bool) error {
	cfg, _ := config.LoadMerged(root)
	projects := detect.DiscoverDotNet(root, cfg.Solution, cfg.Exclude)
	testProject := resolveDotnetTestProject(cfg, root, projects)
	if testProject == "" {
		return fmt.Errorf("no test project found — add one (IsTestProject / Microsoft.NET.Test.Sdk / *Tests), or set test.project in %s", config.FileName)
	}

	filter := testShorthandFilter(args[0])
	if filter == "" {
		classes := discoverTestClassNames(filepath.Dir(testProject))
		matches := matchTestClasses(classes, args[0])
		if len(matches) == 1 {
			echo(cmd, "matched test class: "+matches[0])
		}
		filter = testClassFilter(matches, args[0])
	}

	runner := detectDotnetTestRunner(root, "")
	argv := buildDotnetTestArgs(runner, testProject, filter, "", args[1:], watch, "")
	return runCommand(cmd, root, append([]string{"dotnet"}, argv...))
}

// resolveDotnetTestProject picks the csproj `rig test` runs: the configured
// test.project (resolved against root when relative), else the first
// discovered test project. "" when there is none. Mirrors the .NET rig's
// TestVerb.ResolveTestProject. Pure.
func resolveDotnetTestProject(cfg config.Config, root string, projects []detect.ProjectInfo) string {
	if cfg.Test != nil && strings.TrimSpace(cfg.Test.Project) != "" {
		p := cfg.Test.Project
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		return p
	}
	for _, p := range projects {
		if p.IsTest {
			return p.FullPath
		}
	}
	return ""
}

// matchTestClasses resolves a query against fully-qualified test-class names
// with rig's tiered matcher (exact > prefix > substring > subsequence), scoring
// both the full name and the short (last-dotted-segment) name, and returns the
// classes in the BEST tier only, sorted. The C# TestVerb uses a flat substring
// match plus an interactive multi-select; the tiers replace the prompt so an
// exact class name never drags its substring cousins into the filter. Pure.
func matchTestClasses(classes []string, query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	best := 0
	var matches []string
	for _, c := range classes {
		score := max(fieldScore(c, q), fieldScore(testClassShortName(c), q))
		if score == 0 {
			continue
		}
		if score > best {
			best, matches = score, matches[:0]
		}
		if score == best {
			matches = append(matches, c)
		}
	}
	sort.Strings(matches)
	return matches
}

// testClassFilter builds the `dotnet test --filter` expression for the matched
// classes, mirroring the C# TestVerb.FilterForName shapes:
//
//	one match   → FullyQualifiedName~<ShortName>
//	many        → FullyQualifiedName~<A>|FullyQualifiedName~<B>|…
//	none        → FullyQualifiedName~<query> (the platform may resolve a method)
//
// Pure.
func testClassFilter(matches []string, query string) string {
	if len(matches) == 0 {
		return "FullyQualifiedName~" + query
	}
	parts := make([]string, len(matches))
	for i, m := range matches {
		parts[i] = "FullyQualifiedName~" + testClassShortName(m)
	}
	return strings.Join(parts, "|")
}

// testClassShortName is the segment after the last '.' of a fully-qualified
// class name.
func testClassShortName(fullName string) string {
	if i := strings.LastIndex(fullName, "."); i >= 0 {
		return fullName[i+1:]
	}
	return fullName
}

// discoverTestClassNames scans the test project directory's *.cs sources
// (skipping bin/obj) for test classes and returns their fully-qualified names,
// sorted and deduped. Best-effort: an unreadable tree or file yields whatever
// was found.
func discoverTestClassNames(projectDir string) []string {
	seen := map[string]bool{}
	_ = filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if name := d.Name(); strings.EqualFold(name, "bin") || strings.EqualFold(name, "obj") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".cs") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, c := range parseTestClassNames(string(data)) {
			seen[c] = true
		}
		return nil
	})
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Test markers, matching TestEnumeration: a class is a test class when it (or
// its declaration) carries a class marker, or any of its methods carries a
// method marker (MSTest, NUnit, xUnit).
var (
	testClassAttrs  = map[string]bool{"TestClass": true, "TestFixture": true}
	testMethodAttrs = map[string]bool{"TestMethod": true, "Test": true, "TestCase": true, "Fact": true, "Theory": true}

	csNamespaceRe = regexp.MustCompile(`^\s*namespace\s+([A-Za-z_][\w.]*)`)
	csClassRe     = regexp.MustCompile(`^\s*((?:(?:public|internal|private|protected|sealed|static|partial|abstract|file)\s+)*)class\s+([A-Za-z_]\w*)`)
	csAttrNameRe  = regexp.MustCompile(`\[\s*([A-Za-z_]\w*)`)
)

// parseTestClassNames extracts the fully-qualified test-class names declared
// in one C# source file with a line-based scan: the (last seen) namespace
// qualifies each class; a class counts as a test class when a class marker
// attribute precedes its declaration or a method marker appears after it
// (before the next class). Abstract/static classes are skipped, as in
// TestEnumeration (a static class is abstract in metadata). Pure, best-effort
// — no full C# parse, mirroring rig's convention-first discovery.
func parseTestClassNames(source string) []string {
	ns := ""
	var classes []string      // fully-qualified, in declaration order
	isTest := map[int]bool{}  // classes index → classified as test
	var pendingAttrs []string // attribute names since the last declaration
	last := -1                // index of the most recently declared class

	for _, raw := range strings.Split(source, "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := csNamespaceRe.FindStringSubmatch(line); m != nil {
			ns = m[1]
			continue
		}
		if m := csClassRe.FindStringSubmatch(line); m != nil {
			mods, name := m[1], m[2]
			if strings.Contains(mods, "abstract") || strings.Contains(mods, "static") {
				pendingAttrs = nil
				last = -1 // method markers inside a skipped class mark nothing
				continue
			}
			full := name
			if ns != "" {
				full = ns + "." + name
			}
			classes = append(classes, full)
			last = len(classes) - 1
			for _, a := range pendingAttrs {
				if testClassAttrs[a] {
					isTest[last] = true
				}
			}
			pendingAttrs = nil
			// The declaration line itself may carry inline attributes for the
			// class body (e.g. `class A { [Fact] void M() {} }`); fall through.
		}
		for _, m := range csAttrNameRe.FindAllStringSubmatch(line, -1) {
			a := m[1]
			pendingAttrs = append(pendingAttrs, a)
			if testMethodAttrs[a] && last >= 0 {
				isTest[last] = true // a marked method inside the current class
			}
		}
	}

	var out []string
	for i, c := range classes {
		if isTest[i] {
			out = append(out, c)
		}
	}
	return out
}
