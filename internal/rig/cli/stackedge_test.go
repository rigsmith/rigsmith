package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/jsonc"
)

const stackEdgeTestRepo = `{"upstream":"github.com/acme/Lib","fork":"github.com/you/Lib","trackBranch":"stack/integration","publishPrefix":"You."}`

func TestStackEdgeParsing(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		inline     bool
	}{
		{"dedicated jsonc", "rig.stack.jsonc", false},
		{"dedicated json", "rig.stack.json", false},
		{"inline stack", ".rig.json", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			body := `{"repos":{"lib":` + stackEdgeTestRepo + `}, "edge":{"lib":{
  "feed":"https://feed.example/index.json",
  "exclude":["Lib.Samples"], "maxSize":"50MB",
  "publishFrom":"https://github.com/acme/lib-edge.git", "pushKeySecret":"EDGE_FEED_KEY",
  "adopted":[{"pr":"github.com/acme/Lib/pull/412","commit":"` + strings.Repeat("ab", 20) + `"}]
}}}`
			if tc.inline {
				body = `{"stack":` + body + `}`
			}
			if err := os.WriteFile(filepath.Join(root, tc.file), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			m, _, err := loadStackManifest(root)
			if err != nil {
				t.Fatal(err)
			}
			want := &stackEdge{Feed: "https://feed.example/index.json", Label: "edge", Exclude: []string{"Lib.Samples"}, MaxSize: "50MB", PublishFrom: "github.com/acme/lib-edge", PushKeySecret: "EDGE_FEED_KEY", Adopted: []stackEdgeAdopted{{PR: "github.com/acme/Lib/pull/412", Commit: strings.Repeat("ab", 20)}}}
			if !reflect.DeepEqual(m.Edge["lib"], want) {
				t.Fatalf("edge = %+v, want %+v", m.Edge["lib"], want)
			}
			got, err := os.ReadFile(filepath.Join(root, tc.file))
			if err != nil || string(got) != body {
				t.Fatalf("loading modified the manifest: %v", err)
			}
		})
	}
}

func TestStackEdgeValidation(t *testing.T) {
	sha := strings.Repeat("ab", 20)
	for _, tc := range []struct {
		name, repo, member, edge, want string
	}{
		{name: "minimal", edge: `{"feed":"https://feed.example/index.json"}`},
		{name: "custom label", edge: `{"feed":"f","label":"preview"}`},
		{name: "explicit publishing", repo: strings.Replace(stackEdgeTestRepo, `"publishPrefix":"You."`, `"publishesAs":{"Lib":"Custom.Lib"}`, 1), edge: `{"feed":"f"}`},
		{name: "nonmember", member: "missing", edge: `{"feed":"f"}`, want: "configured member"},
		{name: "null block", edge: `null`, want: "object"},
		{name: "no trackBranch", repo: strings.Replace(stackEdgeTestRepo, `"trackBranch":"stack/integration",`, "", 1), edge: `{"feed":"f"}`, want: "trackBranch"},
		{name: "no publishing", repo: strings.Replace(stackEdgeTestRepo, `,"publishPrefix":"You."`, "", 1), edge: `{"feed":"f"}`, want: "publishPrefix or publishesAs"},
		{name: "empty publishing", repo: strings.Replace(stackEdgeTestRepo, `"publishPrefix":"You."`, `"publishesAs":{}`, 1), edge: `{"feed":"f"}`, want: "publishPrefix or publishesAs"},
		{name: "no feed", edge: `{}`, want: "feed"},
		{name: "bad label", edge: `{"feed":"f","label":"bad label"}`, want: "label"},
		{name: "overflow label", edge: `{"feed":"f","label":"2147483648"}`, want: "label"},
		{name: "secret name", edge: `{"feed":"f","pushKeySecret":"EDGE_FEED_KEY"}`},
		{name: "secret starts with digit", edge: `{"feed":"f","pushKeySecret":"1KEY"}`, want: "secret NAME"},
		{name: "secret value", edge: `{"feed":"f","pushKeySecret":"some-secret=value"}`, want: "secret NAME"},
		{name: "github token", edge: `{"feed":"f","pushKeySecret":"ghp_sensitive"}`, want: "secret NAME"},
		{name: "reserved secret", edge: `{"feed":"f","pushKeySecret":"GITHUB_KEY"}`, want: "secret NAME"},
		{name: "size", edge: `{"feed":"f","maxSize":"50MB"}`},
		{name: "bad size", edge: `{"feed":"f","maxSize":"50wombats"}`, want: "maxSize"},
		{name: "zero size", edge: `{"feed":"f","maxSize":"0MB"}`, want: "maxSize must be positive"},
		{name: "uppercase sha", edge: `{"feed":"f","adopted":[{"pr":"https://github.com/acme/Lib/pull/412","commit":"` + strings.ToUpper(sha) + `"}]}`},
		{name: "short sha", edge: `{"feed":"f","adopted":[{"pr":"github.com/acme/Lib/pull/412","commit":"abc123"}]}`, want: "40-character"},
		{name: "nonhex sha", edge: `{"feed":"f","adopted":[{"pr":"github.com/acme/Lib/pull/412","commit":"` + strings.Repeat("z", 40) + `"}]}`, want: "40-character"},
		{name: "fork PR", edge: `{"feed":"f","adopted":[{"pr":"github.com/you/Lib/pull/412","commit":"` + sha + `"}]}`, want: "upstream"},
		{name: "bad type", edge: `{"feed":"f","exclude":"Lib"}`, want: "parsing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, member := tc.repo, tc.member
			if repo == "" {
				repo = stackEdgeTestRepo
			}
			if member == "" {
				member = "lib"
			}
			root := t.TempDir()
			writeStackManifest(t, root, fmt.Sprintf(`{"repos":{"lib":%s},"edge":{%q:%s}}`, repo, member, tc.edge))
			_, _, err := loadStackManifest(root)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "ghp_sensitive") {
				t.Fatal("validation echoed a credential")
			}
		})
	}
}

func TestStackEdgePublishFrom(t *testing.T) {
	for _, tc := range []struct {
		spec, want string
		invalid    bool
	}{
		{"", "", false},
		{"github.com/acme/lib-edge", "github.com/acme/lib-edge", false},
		{"https://github.com/acme/lib-edge.git", "github.com/acme/lib-edge", false},
		{"git@github.com:acme/lib-edge.git", "github.com/acme/lib-edge", false},
		{"ssh://git@github.com/acme/lib-edge.git", "github.com/acme/lib-edge", false},
		{"github.com/acme", "", true},
		{"github.com//lib-edge", "", true},
		{"/acme/lib-edge", "", true},
		{"github.com/acme/", "", true},
		{"github.com/acme/lib-edge/extra", "", true},
		{"https://https://github.com/acme/lib-edge", "", true},
		{"https://", "", true},
		{"/", "", true},
		{" ", "", true},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			root := t.TempDir()
			writeStackManifest(t, root, fmt.Sprintf(`{"repos":{"lib":%s},"edge":{"lib":{"feed":"f","publishFrom":%q}}}`, stackEdgeTestRepo, tc.spec))
			m, _, err := loadStackManifest(root)
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "publishFrom") {
					t.Fatalf("error = %v, want publishFrom validation error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := m.Edge["lib"].PublishFrom; got != tc.want {
				t.Fatalf("publishFrom = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStackEdgePRUpstream(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"github.com/acme/Lib/pull/412", true},
		{"https://github.com/acme/Lib/pull/412", true},
		{"https://GITHUB.com/ACME/lib/pull/412/", true},
		{"https://other.example/acme/Lib/pull/412", false},
		{"https://github.com/acme/Other/pull/412", false},
		{"https://github.com/acme/Lib/issues/412", false},
		{"https://github.com/acme/Lib/pull/0", false},
		{"https://github.com/acme/Lib/pull/", false},
		{"https://github.com/acme/Lib/pull/+1", false},
		{"https://github.com/acme/Lib/pull/1/files", false},
		{"https://github.com/acme/Lib/pull/1?other=1", false},
		{"https://github.com/acme/Lib/pull/1#fragment", false},
		{"https://user@github.com/acme/Lib/pull/1", false},
		{"https://github.com/acme/Lib/pull/%31", false},
		{"http://github.com/acme/Lib/pull/1", false},
		{"not a URL", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			if got := stackEdgePRUpstream(tc.url, "github.com/acme/Lib"); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStackEdgeEffectiveMaxSize(t *testing.T) {
	for _, tc := range []struct {
		size string
		want uint64
		err  bool
	}{
		{"", 100_000_000, false}, {"100MB", 100_000_000, false},
		{"50MB", 50_000_000, false}, {"200 MB", 200_000_000, false},
		{"100MiB", 104_857_600, false}, {"1.5MB", 1_500_000, false},
		{"1", 1, false}, {"0", 0, true}, {"0B", 0, true},
		{"-1MB", 0, true}, {" ", 0, true}, {"a lot", 0, true},
		{"18446744073709551616B", 0, true},
	} {
		t.Run(tc.size, func(t *testing.T) {
			got, err := (stackEdge{MaxSize: tc.size}).effectiveMaxSize()
			if (err != nil) != tc.err || got != tc.want {
				t.Fatalf("got %d, %v; want %d, error %v", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestStackEdgeExcludeNotes(t *testing.T) {
	for _, tc := range []struct {
		name              string
		exclude, produced []string
		want              []string
	}{
		{"empty", nil, []string{"Lib"}, nil},
		{"match original", []string{"Lib"}, []string{"Lib"}, nil},
		{"republished is not original", []string{"You.Lib"}, []string{"Lib"}, []string{"lib excludes You.Lib, but produces no package called You.Lib"}},
		{"member only", []string{"Other"}, []string{"Lib"}, []string{"lib excludes Other, but produces no package called Other"}},
		{"no packages", []string{"Lib"}, nil, []string{"lib excludes Lib, but produces no package called Lib"}},
		{"stable order and deduplicate", []string{"Z", "A", "Z"}, nil, []string{"lib excludes A, but produces no package called A", "lib excludes Z, but produces no package called Z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (stackEdge{Exclude: tc.exclude}).excludeNotes("lib", tc.produced); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStackEdgeTemplate(t *testing.T) {
	var m stackManifest
	if err := jsonc.Unmarshal([]byte(stackManifestTemplate), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Edge) != 0 {
		t.Fatal("template activated an example edge")
	}
	start := strings.Index(stackManifestTemplate, `  // "edge": {`)
	end := strings.Index(stackManifestTemplate[start:], "\n\n") + start
	lines := strings.Split(stackManifestTemplate[start:end], "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "  // ")
	}
	example := strings.Join(lines, "\n")
	example = strings.ReplaceAll(example, "<full 40-character sha>", strings.Repeat("a", 40))
	// Load the example with the member it documents, proving it is usable JSONC
	// and obeys the same validation as a hand-authored block.
	root := t.TempDir()
	writeStackManifest(t, root, `{"repos":{"some-lib":`+strings.ReplaceAll(stackEdgeTestRepo, "/Lib", "/Some.Lib")+`},`+example+`}`)
	if _, _, err := loadStackManifest(root); err != nil {
		t.Fatal(err)
	}
}
