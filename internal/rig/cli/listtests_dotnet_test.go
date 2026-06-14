// Fixture-backed checks for the .NET test-discovery path. The runner-detection
// test is always on (it only reads the fixtures' global.json). The live
// round-trip against `dotnet test --list-tests` is opt-in — it builds two real
// .NET projects, so it's gated behind RIG_DOTNET_IT=1 and skips when dotnet is
// absent or the build/restore can't run (treated as infra, not a failure).
//
// Fixtures (testdata/dotnet/): vstest/ is a classic xUnit/VSTest project (no
// global.json runner) and mtp/ is an MSTest project under Microsoft.Testing.
// Platform (global.json test.runner + EnableMSTestRunner). They pin the two
// real `--list-tests` output shapes — VSTest prints FQNs, MTP/MSTest prints
// display names only.
package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func fixtureDir(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "dotnet", name))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// detectDotnetTestRunner must read the fixtures' global.json: the mtp fixture
// opts into Microsoft.Testing.Platform, the vstest fixture (no global.json)
// stays classic VSTest. No dotnet required.
func TestDetectDotnetTestRunner_FromFixtures(t *testing.T) {
	if got := detectDotnetTestRunner(fixtureDir(t, "mtp"), ""); got != mtpRunner {
		t.Errorf("mtp fixture: got runner %v, want mtpRunner", got)
	}
	if got := detectDotnetTestRunner(fixtureDir(t, "vstest"), ""); got != vsTestRunner {
		t.Errorf("vstest fixture: got runner %v, want vsTestRunner", got)
	}
}

// Live round-trip: build each fixture and enumerate via the real platform.
// VSTest yields FQNs we parse into classes; MTP/MSTest yields display names, so
// parsing is empty and the source-scan fallback supplies the classes.
func TestListTests_LiveFixtures(t *testing.T) {
	if os.Getenv("RIG_DOTNET_IT") == "" {
		t.Skip("set RIG_DOTNET_IT=1 to run the live dotnet --list-tests integration check")
	}
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet not on PATH")
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	t.Run("vstest yields FQNs", func(t *testing.T) {
		dir := fixtureDir(t, "vstest")
		out, err := capture(cmd, dir, "dotnet", buildListTestsArgs(vsTestRunner, "Vstest.Tests.csproj")...)
		if err != nil {
			t.Skipf("vstest build/list failed (infra): %v\n%s", err, out)
		}
		got := parseListedTestClasses(out)
		for _, want := range []string{"Acme.Vstest.Tests.CalculatorTests", "Acme.Vstest.Tests.StringTests"} {
			if !containsStr(got, want) {
				t.Errorf("missing %q in %v", want, got)
			}
		}
		if containsStr(got, "Acme.Vstest.Tests.Helper") {
			t.Errorf("Helper is not a test class but was enumerated: %v", got)
		}
	})

	t.Run("mtp falls back to the source scan", func(t *testing.T) {
		dir := fixtureDir(t, "mtp")
		out, err := capture(cmd, dir, "dotnet", buildListTestsArgs(mtpRunner, "Mtp.Tests.csproj")...)
		if err != nil {
			t.Skipf("mtp build/list failed (infra): %v\n%s", err, out)
		}
		// Today MSTest/MTP lists display names only, so parsing is empty. If a
		// future runner emits FQNs this would just be a bonus, not a failure.
		if got := parseListedTestClasses(out); len(got) != 0 {
			t.Logf("note: MTP --list-tests now yields FQNs: %v", got)
		}
		fallback := discoverTestClassNames(dir)
		for _, want := range []string{"Acme.Mtp.Tests.WidgetTests", "Acme.Mtp.Tests.GadgetTests"} {
			if !containsStr(fallback, want) {
				t.Errorf("source-scan fallback missing %q in %v", want, fallback)
			}
		}
	})
}
