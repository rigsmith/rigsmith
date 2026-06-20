package cli

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
)

const targetCsproj = `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`

func projectNames(projects []detect.ProjectInfo) []string {
	names := make([]string, len(projects))
	for i, p := range projects {
		names[i] = p.Name
	}
	return names
}

// dotnetReviewProjects is the set a .NET dep command reviews: the whole repo by
// default (respecting solution/exclude), or one named project — the rig run
// <project> model.

func TestDotnetReviewProjects_WholeRepoByDefault(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "ui/src/Tweed.App/Tweed.App.csproj", targetCsproj)
	writeTreeFile(t, root, "csharp-agents/Tweed.Engine/Tweed.Engine.csproj", targetCsproj)
	writeTreeFile(t, root, "ui/src/Halyards.Foundation/Halyards.Foundation.csproj", targetCsproj)

	projects, err := dotnetReviewProjects(root, config.Config{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects) != 3 {
		t.Errorf("got %d projects %v, want all 3 — outdated reviews the whole repo", len(projects), projectNames(projects))
	}
}

func TestDotnetReviewProjects_ScopesToNamedProject(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "ui/src/Tweed.App/Tweed.App.csproj", targetCsproj)
	writeTreeFile(t, root, "csharp-agents/Tweed.Engine/Tweed.Engine.csproj", targetCsproj)

	// Full name and the dotted short name both scope to the one project.
	for _, q := range []string{"Tweed.App", "App"} {
		projects, err := dotnetReviewProjects(root, config.Config{}, q)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", q, err)
		}
		if len(projects) != 1 || projects[0].Name != "Tweed.App" {
			t.Errorf("%q scoped to %v, want [Tweed.App]", q, projectNames(projects))
		}
	}
}

func TestDotnetReviewProjects_RespectsExclude(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "ui/src/Tweed.App/Tweed.App.csproj", targetCsproj)
	writeTreeFile(t, root, "ui/vendor/actipro/Core/Core.csproj", targetCsproj)

	projects, err := dotnetReviewProjects(root, config.Config{Exclude: []string{"ui/vendor/**"}}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if names := projectNames(projects); len(names) != 1 || names[0] != "Tweed.App" {
		t.Errorf("got %v, want [Tweed.App] — excluded vendor project should be dropped", names)
	}
}

func TestDotnetReviewProjects_UnknownNameIsCalledOut(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "App/App.csproj", targetCsproj)
	writeTreeFile(t, root, "Lib/Lib.csproj", targetCsproj)

	_, err := dotnetReviewProjects(root, config.Config{}, "Nope")
	if err == nil {
		t.Fatal("want an error for an unknown project name")
	}
	for _, want := range []string{"Nope", "App", "Lib"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q — should name the bad arg and what rig saw", err.Error(), want)
		}
	}
}

func TestDotnetReviewProjects_NoProjectsErrors(t *testing.T) {
	root := t.TempDir()
	_, err := dotnetReviewProjects(root, config.Config{}, "")
	if err == nil || !strings.Contains(err.Error(), "no .NET projects") {
		t.Errorf("want a guided 'no projects' error, got: %v", err)
	}
}
