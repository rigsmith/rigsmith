package cli

import (
	"reflect"
	"testing"

	"github.com/rigsmith/cli/internal/detect"
)

func TestParseGoListUpdates(t *testing.T) {
	// Concatenated JSON objects, as `go list -m -u -json all` emits.
	stream := `{"Path":"example.com/me","Version":"v0.1.0","Main":true}
{"Path":"github.com/a/b","Version":"v1.2.0","Update":{"Version":"v1.3.0"}}
{"Path":"github.com/c/d","Version":"v2.0.0"}
{"Path":"github.com/e/f","Version":"v0.4.0","Update":{"Version":"v0.5.1"}}`
	got := parseGoListUpdates(stream)
	want := []outdatedDep{
		{name: "github.com/a/b", current: "v1.2.0", latest: "v1.3.0"},
		{name: "github.com/e/f", current: "v0.4.0", latest: "v0.5.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseNpmOutdated(t *testing.T) {
	// npm/pnpm shape: object keyed by name. left-pad has no installed current.
	js := `{
      "left-pad": {"wanted":"1.3.0","latest":"1.3.0"},
      "lodash": {"current":"4.17.20","wanted":"4.17.21","latest":"4.17.21"},
      "uptodate": {"current":"2.0.0","wanted":"2.0.0","latest":"2.0.0"}
    }`
	got := parseNpmOutdated(js)
	want := []outdatedDep{
		{name: "left-pad", current: "—", latest: "1.3.0"},
		{name: "lodash", current: "4.17.20", latest: "4.17.21"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	// Empty report → no deps.
	if d := parseNpmOutdated("{}"); d != nil {
		t.Fatalf("empty report = %+v, want nil", d)
	}
	if d := parseNpmOutdated(""); d != nil {
		t.Fatalf("blank = %+v, want nil", d)
	}
}

func TestParseDotnetOutdated(t *testing.T) {
	js := `{
      "projects": [
        {"path":"/r/App.csproj","frameworks":[
          {"framework":"net8.0","topLevelPackages":[
            {"id":"Newtonsoft.Json","resolvedVersion":"12.0.0","latestVersion":"13.0.3"},
            {"id":"Serilog","resolvedVersion":"3.0.0","latestVersion":"3.0.0"}
          ]},
          {"framework":"net9.0","topLevelPackages":[
            {"id":"Newtonsoft.Json","resolvedVersion":"12.0.0","latestVersion":"13.0.3"}
          ]}
        ]}
      ]
    }`
	got := parseDotnetOutdated(js)
	want := []outdatedDep{
		{name: "Newtonsoft.Json", current: "12.0.0", latest: "13.0.3", project: "/r/App.csproj"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestGoUpgradeCommands(t *testing.T) {
	deps := []outdatedDep{
		{name: "github.com/a/b", latest: "v1.3.0"},
		{name: "github.com/c/d", latest: "v2.1.0"},
	}
	got := goUpgradeCommands(deps)
	want := [][]string{
		{"go", "get", "github.com/a/b@v1.3.0", "github.com/c/d@v2.1.0"},
		{"go", "mod", "tidy"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if goUpgradeCommands(nil) != nil {
		t.Fatal("empty deps should produce no commands")
	}
}

func TestNodeUpgradeCommands(t *testing.T) {
	deps := []outdatedDep{{name: "lodash", latest: "4.17.21"}}
	// npm uses install; others use add.
	if got := nodeUpgradeCommands(detect.NPM, deps); !reflect.DeepEqual(got, [][]string{{"npm", "install", "lodash@4.17.21"}}) {
		t.Fatalf("npm = %+v", got)
	}
	if got := nodeUpgradeCommands(detect.PNPM, deps); !reflect.DeepEqual(got, [][]string{{"pnpm", "add", "lodash@4.17.21"}}) {
		t.Fatalf("pnpm = %+v", got)
	}
}

func TestDotnetUpgradeCommands(t *testing.T) {
	deps := []outdatedDep{
		{name: "Newtonsoft.Json", latest: "13.0.3", project: "/r/App.csproj"},
		{name: "Serilog", latest: "3.1.1"}, // no project → operate on cwd
	}
	got := dotnetUpgradeCommands(deps)
	want := [][]string{
		{"dotnet", "add", "/r/App.csproj", "package", "Newtonsoft.Json", "--version", "13.0.3"},
		{"dotnet", "add", "package", "Serilog", "--version", "3.1.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}
