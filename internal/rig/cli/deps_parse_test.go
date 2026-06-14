package cli

import (
	"reflect"
	"testing"
)

func TestParseGoListAll(t *testing.T) {
	// `go list -m -u -json all`: main module skipped; one outdated, one current.
	stream := `{"Path":"example.com/me","Version":"v0.1.0","Main":true}
{"Path":"github.com/a/b","Version":"v1.2.0","Update":{"Version":"v1.3.0"}}
{"Path":"github.com/c/d","Version":"v2.0.0"}`
	got := parseGoListAll(stream)
	want := []outdatedDep{
		{name: "github.com/a/b", current: "v1.2.0", latest: "v1.3.0"},
		{name: "github.com/c/d", current: "v2.0.0", latest: "v2.0.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseDotnetList(t *testing.T) {
	js := `{"projects":[{"path":"/p/App.csproj","frameworks":[{"topLevelPackages":[
	  {"id":"Newtonsoft.Json","resolvedVersion":"13.0.4"},
	  {"id":"Serilog","resolvedVersion":"3.1.1"}]}]}]}`
	got := parseDotnetList(js)
	want := []outdatedDep{
		{name: "Newtonsoft.Json", current: "13.0.4", project: "/p/App.csproj"},
		{name: "Serilog", current: "3.1.1", project: "/p/App.csproj"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseNpmList(t *testing.T) {
	// npm folds prod and dev into one `dependencies` map.
	js := `{"name":"x","version":"1.0.0","dependencies":{
	  "lodash":{"version":"4.17.20"},
	  "left-pad":{"version":"1.3.0"}}}`
	got := parseNpmList(js)
	want := []outdatedDep{
		{name: "left-pad", current: "1.3.0"},
		{name: "lodash", current: "4.17.20"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParsePnpmList(t *testing.T) {
	// pnpm: array of projects, separate dependencies / devDependencies maps.
	js := `[{"name":"x","version":"1.0.0",
	  "dependencies":{"lodash":{"version":"4.17.20"}},
	  "devDependencies":{"left-pad":{"version":"1.3.0"}}}]`
	got := parsePnpmList(js)
	want := []outdatedDep{
		{name: "left-pad", current: "1.3.0", dev: true},
		{name: "lodash", current: "4.17.20"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseBunList(t *testing.T) {
	// bun pm ls text tree; banner skipped, scoped name keeps its leading @.
	text := "/repo node_modules (3)\n" +
		"├── is-odd@3.0.0\n" +
		"├── @scope/thing@2.1.0\n" +
		"└── lodash@4.17.20\n"
	got := parseBunList(text)
	want := []outdatedDep{
		{name: "@scope/thing", current: "2.1.0"},
		{name: "is-odd", current: "3.0.0"},
		{name: "lodash", current: "4.17.20"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseYarnClassicList(t *testing.T) {
	// yarn v1 `yarn list --depth=0 --json`: a tree message with name@version.
	text := `{"type":"activityStart","data":{"id":0}}
{"type":"tree","data":{"type":"list","trees":[{"name":"lodash@4.17.20"},{"name":"@babel/core@7.0.0"}]}}`
	got := parseYarnClassicList(text)
	want := []outdatedDep{
		{name: "@babel/core", current: "7.0.0"},
		{name: "lodash", current: "4.17.20"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestMergeLatest(t *testing.T) {
	all := []outdatedDep{
		{name: "lodash", current: "4.17.20"},
		{name: "left-pad", current: "1.3.0"},
	}
	outdated := []outdatedDep{
		{name: "lodash", current: "4.17.20", latest: "4.18.1"},
	}
	got := mergeLatest(all, outdated, false)
	want := []outdatedDep{
		{name: "left-pad", current: "1.3.0", latest: "1.3.0"}, // no update → latest == current
		{name: "lodash", current: "4.17.20", latest: "4.18.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestMergeLatestByProject(t *testing.T) {
	// Same package id in two projects, only one outdated — keyed by project+name.
	all := []outdatedDep{
		{name: "Serilog", current: "3.0.0", project: "/a.csproj"},
		{name: "Serilog", current: "3.1.1", project: "/b.csproj"},
	}
	outdated := []outdatedDep{
		{name: "Serilog", current: "3.0.0", latest: "4.0.0", project: "/a.csproj"},
	}
	got := mergeLatest(all, outdated, true)
	want := []outdatedDep{
		{name: "Serilog", current: "3.0.0", latest: "4.0.0", project: "/a.csproj"},
		{name: "Serilog", current: "3.1.1", latest: "3.1.1", project: "/b.csproj"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestHasUpdate(t *testing.T) {
	cases := []struct {
		dep  outdatedDep
		want bool
	}{
		{outdatedDep{current: "1.0.0", latest: "1.1.0"}, true},
		{outdatedDep{current: "1.0.0", latest: "1.0.0"}, false},
		{outdatedDep{current: "1.0.0", latest: ""}, false},
	}
	for _, c := range cases {
		if got := c.dep.hasUpdate(); got != c.want {
			t.Errorf("hasUpdate(%+v) = %v, want %v", c.dep, got, c.want)
		}
	}
}
