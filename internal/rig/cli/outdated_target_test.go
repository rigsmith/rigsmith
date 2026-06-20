package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/config"
)

const targetCsproj = `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`

// dotnetListTarget resolves what `dotnet list package` should inspect from a
// workspace root, so `rig outdated`/`rig deps` work without cd-ing into a
// project — and explains the fix when nothing resolves.

func TestDotnetListTarget_RootSolutionWins(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "App/App.csproj", targetCsproj)
	writeTreeFile(t, root, "App.slnx", `<Solution><Project Path="App/App.csproj" /></Solution>`)

	got, err := dotnetListTarget(root, config.Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(got, "App.slnx") {
		t.Errorf("target = %q, want the root solution", got)
	}
}

func TestDotnetListTarget_DefaultProjectInSubdir(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "ui/src/Tweed.App/Tweed.App.csproj", targetCsproj)
	writeTreeFile(t, root, "csharp-agents/Tweed.Engine/Tweed.Engine.csproj", targetCsproj)

	// No solution at the root; the configured defaultProject names the target.
	got, err := dotnetListTarget(root, config.Config{DefaultProject: "Tweed.App"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(got) != "Tweed.App.csproj" {
		t.Errorf("target = %q, want Tweed.App.csproj", got)
	}
}

func TestDotnetListTarget_LoneProjectNeedsNoConfig(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "src/Only/Only.csproj", targetCsproj)

	got, err := dotnetListTarget(root, config.Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(got) != "Only.csproj" {
		t.Errorf("target = %q, want Only.csproj", got)
	}
}

func TestDotnetListTarget_AmbiguousGivesActionableError(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "App/App.csproj", targetCsproj)
	writeTreeFile(t, root, "Lib/Lib.csproj", targetCsproj)

	_, err := dotnetListTarget(root, config.Config{})
	if err == nil {
		t.Fatal("want an error when several projects exist and nothing selects one")
	}
	msg := err.Error()
	for _, want := range []string{"solution", "defaultProject", "App", "Lib"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q — should name the fix and what rig saw", msg, want)
		}
	}
}

func TestDotnetListTarget_UnmatchedDefaultIsCalledOut(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "App/App.csproj", targetCsproj)
	writeTreeFile(t, root, "Lib/Lib.csproj", targetCsproj)

	_, err := dotnetListTarget(root, config.Config{DefaultProject: "Nope"})
	if err == nil || !strings.Contains(err.Error(), "Nope") {
		t.Errorf("error should name the unmatched defaultProject, got: %v", err)
	}
}
