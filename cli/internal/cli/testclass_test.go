// Tests for the `rig test <name>` test-class resolution (the .NET rig's
// TestVerb.FilterForName / TestEnumeration.IsTestClass): source scanning,
// tiered matching, and --filter expression shapes. Hermetic — fixture sources
// live in temp dirs.
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
)

// ---- parseTestClassNames -----------------------------------------------

func TestParseTestClassNames_MSTestClassAttribute(t *testing.T) {
	src := `namespace Acme.Tests;

[TestClass]
public class KillVerbTests
{
    [TestMethod] public void Parses() { }
}

public class Helper { }
`
	eqSlice(t, parseTestClassNames(src), []string{"Acme.Tests.KillVerbTests"})
}

func TestParseTestClassNames_XunitAndNUnitMethodMarkers(t *testing.T) {
	src := `namespace Acme.Tests
{
    public class FactTests
    {
        [Fact] public void Works() { }
    }

    [TestFixture]
    public class FixtureTests { }

    public class CaseTests
    {
        [TestCase(1)] public void Cases(int n) { }
    }

    public class PlainHelper
    {
        public void NotATest() { }
    }
}
`
	eqSlice(t, parseTestClassNames(src),
		[]string{"Acme.Tests.FactTests", "Acme.Tests.FixtureTests", "Acme.Tests.CaseTests"})
}

func TestParseTestClassNames_SkipsAbstractAndStaticClasses(t *testing.T) {
	// TestEnumeration skips !IsClass / IsAbstract (a static class is abstract
	// in metadata); a method marker inside a skipped class marks nothing.
	src := `namespace N;
public abstract class BaseTests
{
    [TestMethod] public void Shared() { }
}
public static class Helpers { }
[TestClass]
public sealed partial class RealTests { }
`
	eqSlice(t, parseTestClassNames(src), []string{"N.RealTests"})
}

func TestParseTestClassNames_NoNamespaceUsesBareName(t *testing.T) {
	src := "[TestClass]\nclass GlobalTests { }\n"
	eqSlice(t, parseTestClassNames(src), []string{"GlobalTests"})
}

func TestParseTestClassNames_AttributeAfterOneClassBelongsToTheNext(t *testing.T) {
	src := `namespace N;
public class First
{
    [Fact] public void A() { }
}

[TestClass]
public class Second { }
`
	// First is a test via its [Fact]; the [TestClass] between them marks
	// Second, not First.
	eqSlice(t, parseTestClassNames(src), []string{"N.First", "N.Second"})
}

// ---- discoverTestClassNames ----------------------------------------------

func TestDiscoverTestClassNames_ScansSourcesSkippingBinObj(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("AlphaTests.cs", "namespace P;\n[TestClass]\npublic class AlphaTests { }\n")
	write("nested/BetaTests.cs", "namespace P.Nested;\npublic class BetaTests { [Fact] public void X() { } }\n")
	write("bin/Debug/Stale.cs", "namespace P;\n[TestClass]\npublic class StaleTests { }\n")
	write("obj/Gen.cs", "namespace P;\n[TestClass]\npublic class GenTests { }\n")
	write("notes.txt", "[TestClass] class NotCSharp { }")

	eqSlice(t, discoverTestClassNames(dir), []string{"P.AlphaTests", "P.Nested.BetaTests"})
}

// ---- matchTestClasses (tiered: exact > prefix > substring > subsequence) --

func TestMatchTestClasses_ExactShortNameBeatsSubstringCousins(t *testing.T) {
	classes := []string{"Acme.KillTests", "Acme.KillTestsExtra", "Acme.OtherTests"}
	eqSlice(t, matchTestClasses(classes, "KillTests"), []string{"Acme.KillTests"})
	eqSlice(t, matchTestClasses(classes, "killtests"), []string{"Acme.KillTests"}) // case-insensitive
}

func TestMatchTestClasses_PrefixThenSubstringThenSubsequence(t *testing.T) {
	classes := []string{"Acme.KillVerbTests", "Acme.OverkillTests", "Acme.KveryLongIdeaTests"}
	// prefix tier: KillVerbTests starts with "kill"; Overkill only contains it.
	eqSlice(t, matchTestClasses(classes, "Kill"), []string{"Acme.KillVerbTests"})
	// substring tier when no exact/prefix hit.
	eqSlice(t, matchTestClasses(classes, "verkill"), []string{"Acme.OverkillTests"})
	// subsequence as the last resort.
	eqSlice(t, matchTestClasses(classes, "kvlit"), []string{"Acme.KveryLongIdeaTests"})
}

func TestMatchTestClasses_TiesWithinATierAreAllReturnedSorted(t *testing.T) {
	classes := []string{"B.AuthTests", "A.AuthTests", "C.Unrelated"}
	eqSlice(t, matchTestClasses(classes, "AuthTests"), []string{"A.AuthTests", "B.AuthTests"})
}

func TestMatchTestClasses_NoMatchAndEmptyQuery(t *testing.T) {
	classes := []string{"Acme.KillTests"}
	if got := matchTestClasses(classes, "zzz"); len(got) != 0 {
		t.Fatalf("matchTestClasses(zzz) = %v, want empty", got)
	}
	if got := matchTestClasses(classes, "  "); len(got) != 0 {
		t.Fatalf("matchTestClasses(blank) = %v, want empty", got)
	}
}

// ---- testClassFilter -------------------------------------------------------

func TestTestClassFilter_ShapesMirrorTheCSharpTestVerb(t *testing.T) {
	// One match → FullyQualifiedName~ShortName.
	if got := testClassFilter([]string{"Acme.KillTests"}, "Kill"); got != "FullyQualifiedName~KillTests" {
		t.Fatalf("one match: %q", got)
	}
	// Several → joined with | (the C# multi-select's "accept all" shape).
	got := testClassFilter([]string{"A.AuthTests", "B.AuthTests"}, "Auth")
	if got != "FullyQualifiedName~AuthTests|FullyQualifiedName~AuthTests" {
		t.Fatalf("many matches: %q", got)
	}
	// None → fall through to the platform with the raw query (could be a method).
	if got := testClassFilter(nil, "MyMethod"); got != "FullyQualifiedName~MyMethod" {
		t.Fatalf("no match: %q", got)
	}
}

// ---- resolveDotnetTestProject ----------------------------------------------

func TestResolveDotnetTestProject_ConfigOverrideWins(t *testing.T) {
	projects := []detect.ProjectInfo{{Name: "App.Tests", FullPath: "/r/App.Tests/App.Tests.csproj", IsTest: true}}

	cfg := config.Config{Test: &config.Test{Project: "tests/Pinned.csproj"}}
	want := filepath.Join("/r", "tests/Pinned.csproj")
	if got := resolveDotnetTestProject(cfg, "/r", projects); got != want {
		t.Fatalf("relative override: %q, want %q", got, want)
	}

	abs := filepath.Join(string(filepath.Separator), "abs", "T.csproj")
	cfg = config.Config{Test: &config.Test{Project: abs}}
	if got := resolveDotnetTestProject(cfg, "/r", projects); got != abs {
		t.Fatalf("absolute override: %q, want %q", got, abs)
	}
}

func TestResolveDotnetTestProject_FirstTestProjectElseEmpty(t *testing.T) {
	projects := []detect.ProjectInfo{
		{Name: "App", FullPath: "/r/App/App.csproj", OutputType: "Exe"},
		{Name: "App.Tests", FullPath: "/r/App.Tests/App.Tests.csproj", IsTest: true},
	}
	if got := resolveDotnetTestProject(config.Config{}, "/r", projects); got != "/r/App.Tests/App.Tests.csproj" {
		t.Fatalf("first test project: %q", got)
	}
	if got := resolveDotnetTestProject(config.Config{}, "/r", projects[:1]); got != "" {
		t.Fatalf("no test project: %q, want empty", got)
	}
}
