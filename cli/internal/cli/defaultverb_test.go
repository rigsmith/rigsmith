// Port of the .NET rig's DefaultVerb behavior, against runDefault. The
// interactive picker paths need a TTY and aren't exercised here; under `go
// test` stdin isn't a terminal, so the non-interactive branches run — exactly
// the C# AnsiConsole non-interactive paths.
package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/cli/internal/config"
	"github.com/spf13/cobra"
)

// defaultRepo builds a temp repo with the given runnable projects (and a .slnx
// naming them), isolated from the developer's real ~/.rig.json.
func defaultRepo(t *testing.T, projects ...string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("RIG_GLOBAL_CONFIG", filepath.Join(root, "global-rig.json"))
	var sln strings.Builder
	sln.WriteString("<Solution>")
	for _, p := range projects {
		writeTreeFile(t, root, p+"/"+p+".csproj", conventionExeCsproj)
		sln.WriteString(`<Project Path="` + p + `/` + p + `.csproj" />`)
	}
	sln.WriteString("</Solution>")
	writeTreeFile(t, root, "App.slnx", sln.String())
	return root
}

// defaultHost is a bare command hosting runDefault with captured output.
func defaultHost() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	return cmd, &buf
}

func TestDefault_NoArgNonInteractivePrintsTheGuidanceWhenUnset(t *testing.T) {
	root := defaultRepo(t, "App")
	cmd, buf := defaultHost()

	if err := runDefault(cmd, root, ""); err != nil {
		t.Fatalf("runDefault: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "No default project set.") {
		t.Fatalf("output = %q, want the no-default guidance", got)
	}
}

func TestDefault_NoArgNonInteractivePrintsTheCurrentValue(t *testing.T) {
	root := defaultRepo(t, "App")
	writeTreeFile(t, root, config.FileName, `{ "defaultProject": "App" }`)
	cmd, buf := defaultHost()

	if err := runDefault(cmd, root, ""); err != nil {
		t.Fatalf("runDefault: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "defaultProject = App") {
		t.Fatalf("output = %q, want the current default", got)
	}
}

func TestDefault_QueryValidatesAndPersistsViaTheConfigWriter(t *testing.T) {
	root := defaultRepo(t, "App.Web", "App.Cli")
	cmd, buf := defaultHost()

	if err := runDefault(cmd, root, "web"); err != nil {
		t.Fatalf("runDefault: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "Set defaultProject = App.Web in .rig.json") {
		t.Fatalf("output = %q, want the success line", got)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load back: %v", err)
	}
	if cfg.DefaultProject != "App.Web" {
		t.Fatalf("persisted defaultProject = %q, want App.Web", cfg.DefaultProject)
	}
}

func TestDefault_UnknownQueryFailsAndListsTheRunnableProjects(t *testing.T) {
	root := defaultRepo(t, "App")
	cmd, _ := defaultHost()

	err := runDefault(cmd, root, "nope")
	if err == nil {
		t.Fatal("an unknown query should fail")
	}
	if msg := err.Error(); !strings.Contains(msg, "no project matches 'nope'") || !strings.Contains(msg, "App") {
		t.Fatalf("error = %q, want the no-match message with the runnable list", msg)
	}
	if _, statErr := os.Stat(filepath.Join(root, config.FileName)); !os.IsNotExist(statErr) {
		t.Fatal("a failed set must write nothing")
	}
}

func TestDefault_AmbiguousQueryWhenPipedFailsListingTheMatches(t *testing.T) {
	root := defaultRepo(t, "App.Web", "App.Cli")
	cmd, _ := defaultHost()

	err := runDefault(cmd, root, "app")
	if err == nil {
		t.Fatal("an ambiguous query should fail without a TTY")
	}
	msg := err.Error()
	if !strings.Contains(msg, "matches multiple projects") ||
		!strings.Contains(msg, "App.Web") || !strings.Contains(msg, "App.Cli") {
		t.Fatalf("error = %q, want the ambiguity message listing both matches", msg)
	}
	if _, statErr := os.Stat(filepath.Join(root, config.FileName)); !os.IsNotExist(statErr) {
		t.Fatal("an ambiguous set must write nothing")
	}
}
