package cli

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
)

func TestStackEdgeHookGolden(t *testing.T) {
	got := stackEdgeHookXML(map[string]string{"Lib.Controls": "You.Lib.Controls", "Lib": "You.Lib"}, nil)
	want, err := os.ReadFile("testdata/edge-hook.targets")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("hook:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(string(got), "GlobalPropertiesToRemove") {
		t.Fatal("hook must propagate to project references")
	}
}

func TestStackEdgeHookChainsPerProject(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "Lib & project.csproj")
	other := filepath.Join(root, "Other.csproj")
	tools := filepath.Join(root, "tools")
	originals := map[string]dotnet.PackMetadata{
		project: {OriginalHook: "original & hook.targets", ToolsPath: tools},
		other:   {},
	}
	path, cleanup, err := stackEdgeWriteHook(map[string]string{"Explicit.Id": "You.Explicit.Id"}, originals)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if filepath.Dir(path) == root {
		t.Fatal("hook was written into tree")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Groups []struct {
			Condition string `xml:"Condition,attr"`
			Original  string `xml:"_RigEdgeOriginalHook"`
			ID        string `xml:"PackageId"`
		} `xml:"PropertyGroup"`
		Imports []struct {
			Project string `xml:"Project,attr"`
		} `xml:"Import"`
	}
	if err := xml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Groups) != 2 || document.Groups[0].Original != filepath.Join(tools, "original & hook.targets") || !strings.Contains(document.Groups[0].Condition, project) {
		t.Fatalf("chaining: %s", data)
	}
	if len(document.Imports) != 1 || document.Imports[0].Project != "$(_RigEdgeOriginalHook)" {
		t.Fatalf("imports: %+v", document.Imports)
	}
	if document.Groups[1].ID != "You.Explicit.Id" || !strings.Contains(document.Groups[1].Condition, "Explicit.Id") || strings.Contains(document.Groups[1].Condition, "MSBuildProjectName") {
		t.Fatalf("rename uses wrong identity: %+v", document.Groups[1])
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("hook leaked: %v", err)
	}
}
