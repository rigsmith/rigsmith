package scripts

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gowork"
)

// Every way a tool reaches a machine, and how to read the list out of each one.
//
// The installers that DISCOVER their tools — dev-install and source-install,
// both via gowork.Tools — are deliberately absent: there is nothing there to
// forget. These are the hand-maintained lists, and the point of this test is
// that adding cmd/<tool> without touching them is caught here rather than by
// somebody wondering why `brew install <tool>` says no such cask.
//
// Each rule extracts the surface's OWN list rather than grepping for the name,
// so a tool merely mentioned in a comment cannot satisfy it. The comparison is
// containment, not equality: several of these legitimately carry entries that
// are not cmd/ tools — the `rigsmith` bundle, the `clauderig-ui` window.
var distSurfaces = []struct {
	file string
	// section narrows the file to the block holding the list, when the pattern
	// alone would be ambiguous. Empty means the whole file.
	section *regexp.Regexp
	list    *regexp.Regexp
}{
	{file: "install.sh", list: regexp.MustCompile(`install_binary (\w+)`)},
	{file: "install.ps1", section: regexp.MustCompile(`(?m)^\s*\$binaries = @\(.*\)$`), list: regexp.MustCompile(`'([\w-]+)'`)},
	{file: "brew.sh", section: regexp.MustCompile(`(?m)^\s*[\w|-]+\) cask=`), list: regexp.MustCompile(`([\w-]+)[|)]`)},
	{file: "winres.sh", section: regexp.MustCompile(`(?m)^\s*for tool in .*; do$`), list: regexp.MustCompile(`\b(\w+rig|rig)\b`)},
	// No ^ anchor: the first entry shares its line with `packages="${WINGET_PACKAGES:-`.
	// The bundle's line ends in `}"` rather than the name, so it falls out here, which
	// is right — `rigsmith` is not a cmd/ tool.
	{file: "winget-submit.sh", list: regexp.MustCompile(`(?m)RigSmith\.\w+:([\w-]+)$`)},
	{file: filepath.Join("npm", "build-packages.mjs"), section: regexp.MustCompile(`(?s)const TOOLS = \{.*?\n\}`), list: regexp.MustCompile(`(?m)^\s*([\w-]+):`)},
	{file: filepath.Join("..", ".goreleaser.yaml"), list: regexp.MustCompile(`main: \./cmd/(\w+)`)},
}

// A new cmd/<tool> has to reach every distribution surface, and nothing but this
// says so. install.sh, install.ps1, brew.sh, winres.sh, winget-submit.sh, the npm
// packages and .goreleaser.yaml each carry the tool names written out by hand;
// codexrig landed in all seven because somebody was careful, which is not a
// mechanism. CLAUDE.md's list of what a change has to update exists because each
// of its rows was forgotten at least once.
func TestEveryToolReachesEveryDistributionSurface(t *testing.T) {
	repo, err := gowork.FindRoot("..")
	if err != nil {
		t.Fatal(err)
	}
	tools, err := gowork.Tools(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) == 0 {
		t.Fatal("no tools discovered under cmd/ — this test would pass vacuously")
	}

	for _, s := range distSurfaces {
		raw, err := os.ReadFile(s.file)
		if err != nil {
			t.Errorf("%s: %v", s.file, err)
			continue
		}
		hay := string(raw)
		if s.section != nil {
			hay = strings.Join(s.section.FindAllString(hay, -1), "\n")
			if hay == "" {
				t.Errorf("%s: the block holding the tool list no longer matches — fix this test's section pattern, "+
					"otherwise it silently stops checking anything", s.file)
				continue
			}
		}
		listed := map[string]bool{}
		for _, m := range s.list.FindAllStringSubmatch(hay, -1) {
			listed[m[1]] = true
		}
		if len(listed) == 0 {
			t.Errorf("%s: no tool names extracted — fix this test's pattern rather than deleting it", s.file)
			continue
		}
		var missing []string
		for _, tool := range tools {
			if !listed[tool.Name] {
				missing = append(missing, tool.Name)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("%s does not install/list %s — a tool under cmd/ that never reaches this surface "+
				"is invisible to whoever installs that way (it listed: %s)",
				s.file, strings.Join(missing, ", "), strings.Join(sortedKeys(listed), ", "))
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
