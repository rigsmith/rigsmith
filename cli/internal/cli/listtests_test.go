// Tests for the platform-discovery test enumeration: the `--list-tests` arg
// shapes per runner and the format-agnostic output parser (VSTest + MTP).
package cli

import "testing"

// ---- buildListTestsArgs ------------------------------------------------

func TestBuildListTestsArgs_VSTestPositional(t *testing.T) {
	eqSlice(t, buildListTestsArgs(vsTestRunner, "tests/Acme.Tests.csproj"),
		[]string{"test", "tests/Acme.Tests.csproj", "--list-tests"})
}

func TestBuildListTestsArgs_MTPUsesProjectFlag(t *testing.T) {
	eqSlice(t, buildListTestsArgs(mtpRunner, "tests/Acme.Tests.csproj"),
		[]string{"test", "--project", "tests/Acme.Tests.csproj", "--list-tests"})
}

// ---- parseListedTestClasses --------------------------------------------

func TestParseListedTestClasses_VSTestOutput(t *testing.T) {
	out := `Microsoft (R) Test Execution Command Line Tool Version 17.11.0
Copyright (c) Microsoft Corporation.  All rights reserved.

The following Tests are available:
    Acme.Tests.KillVerbTests.Parses
    Acme.Tests.KillVerbTests.Guards
    Acme.Tests.Sub.CdTests.Matches
`
	eqSlice(t, parseListedTestClasses(out),
		[]string{"Acme.Tests.KillVerbTests", "Acme.Tests.Sub.CdTests"})
}

func TestParseListedTestClasses_TheoryDataAndNestedTypes(t *testing.T) {
	// MTP/theory rows can append data args (with spaces) and nested types use
	// '+'; the class FQN must still come out clean.
	out := `Acme.Tests.MathTests.Adds(a: 1, b: 2)
Acme.Tests.Outer+Inner.Runs
`
	eqSlice(t, parseListedTestClasses(out),
		[]string{"Acme.Tests.MathTests", "Acme.Tests.Outer.Inner"})
}

func TestParseListedTestClasses_MTPDisplayNamesYieldNothing(t *testing.T) {
	// MSTest under Microsoft.Testing.Platform lists bare display names (no FQN),
	// so class extraction is empty and the caller falls back to the source scan.
	// (Real `dotnet test --project … --list-tests` output, MSTest 3.11.)
	out := `Discovered 4 tests in assembly - /repo/bin/Debug/net8.0/Mtp.Tests.dll (net8.0|arm64)
  Builds
  Counts (3)
  Counts (4)
  Works

Discovered 4 tests.
`
	if got := parseListedTestClasses(out); len(got) != 0 {
		t.Fatalf("expected no classes from MTP display names, got %v", got)
	}
}

func TestParseListedTestClasses_RejectsNoiseLines(t *testing.T) {
	// Restore/build/banner/path lines must not be mistaken for test FQNs.
	out := `  Determining projects to restore...
  Restored /home/u/Acme.Tests.csproj (in 1.2 sec).
Build succeeded.
Nondotted
`
	if got := parseListedTestClasses(out); len(got) != 0 {
		t.Fatalf("expected no classes from noise, got %v", got)
	}
}
