package scripts

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
type surface struct {
	file string
	// what names the list when a file holds more than one, so a failure says
	// which of them is missing the tool.
	what string
	// section narrows the file to the block holding the list, when the pattern
	// alone would be ambiguous. Empty means the whole file. If it captures, only
	// group 1 is kept — which is how a list embedded in a line of shell keeps
	// `for`, `tool`, `in` and `do` out of the extracted names.
	section *regexp.Regexp
	list    *regexp.Regexp
	// windowsOnly marks a surface that only ever ships Windows artifacts. A
	// tool with no Windows build cannot reach it, and demanding that it does
	// would be demanding a binary that cannot exist.
	//
	// Which tools that exempts is NOT written down here — it is read out of
	// .goreleaser.yaml's goos lists. Hardcoding a name would quietly keep
	// exempting it after someone added the Windows build.
	windowsOnly bool
}

var distSurfaces = []surface{
	{file: "install.sh", list: regexp.MustCompile(`install_binary ([\w-]+)`)},
	{file: "install.ps1", windowsOnly: true, section: regexp.MustCompile(`(?m)^\s*\$binaries = @\(.*\)$`), list: regexp.MustCompile(`'([\w-]+)'`)},
	{file: "brew.sh", section: regexp.MustCompile(`(?m)^\s*[\w|-]+\) cask=`), list: regexp.MustCompile(`([\w-]+)[|)]`)},
	{file: "winres.sh", windowsOnly: true, section: regexp.MustCompile(`(?m)^\s*for tool in (.*); do$`), list: regexp.MustCompile(`([\w-]+)`)},
	// No ^ anchor: the first entry shares its line with `packages="${WINGET_PACKAGES:-`.
	// The bundle's line ends in `}"` rather than the name, so it falls out here, which
	// is right — `rigsmith` is not a cmd/ tool.
	{file: "winget-submit.sh", windowsOnly: true, list: regexp.MustCompile(`(?m)RigSmith\.\w+:([\w-]+)$`)},
	{file: filepath.Join("npm", "build-packages.mjs"), section: regexp.MustCompile(`(?s)const TOOLS = \{.*?\n\}`), list: regexp.MustCompile(`(?m)^\s*'?([\w-]+)'?:`)},
	{file: filepath.Join("..", ".goreleaser.yaml"), what: "builds", list: regexp.MustCompile(`main: \./cmd/([\w-]+)`)},

	// A build is not a shipped artifact. `builds` only compiles it; `archives`,
	// `homebrew_casks` and `scoops` each decide separately whether it reaches
	// anyone, and a tool can be built and then shipped by none of them.
	{file: filepath.Join("..", ".goreleaser.yaml"), what: "archives", list: regexp.MustCompile(`name_template: "([\w-]+)_\{\{ \.Version`)},
	{file: filepath.Join("..", ".goreleaser.yaml"), what: "casks and scoops", list: regexp.MustCompile(`(?m)^\s+- name: ([\w-]+)$`)},

	// `curl rigsmith.sh/<tool> | sh` is a public installer route, and the edge
	// function gates it on a list written out by hand: a tool missing from TOOLS
	// is refused and redirected to the docs instead of being installed.
	{file: filepath.Join("..", "site", "netlify", "edge-functions", "install.ts"), what: "TOOLS",
		section: regexp.MustCompile(`(?m)^const TOOLS = new Set\(\[.*\]\)$`), list: regexp.MustCompile(`'([\w-]+)'`)},
	{file: filepath.Join("..", "site", "netlify", "edge-functions", "install.ts"), what: "DOCS_PATH",
		section: regexp.MustCompile(`(?s)const DOCS_PATH: Record<string, string> = \{.*?\n\}`), list: regexp.MustCompile(`(?m)^\s+'?([\w-]+)'?:`)},
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

	onWindows, err := toolsBuiltForWindows(filepath.Join("..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range distSurfaces {
		raw, err := os.ReadFile(s.file)
		if err != nil {
			t.Errorf("%s: %v", s.label(), err)
			continue
		}
		listed, err := s.toolsListed(raw)
		if err != nil {
			t.Errorf("%s: %v", s.label(), err)
			continue
		}
		var missing []string
		for _, tool := range tools {
			if s.windowsOnly && !onWindows[tool.Name] {
				continue // no Windows binary exists to ship here
			}
			if !listed[tool.Name] {
				missing = append(missing, tool.Name)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("%s does not install/list %s — a tool under cmd/ that never reaches this surface "+
				"is invisible to whoever installs that way (it listed: %s)",
				s.label(), strings.Join(missing, ", "), strings.Join(sortedKeys(listed), ", "))
		}
	}
}

// toolsBuiltForWindows reads .goreleaser.yaml's builds and reports which cmd/
// tools are compiled for Windows at all.
//
// The Windows-only surfaces are exempted against this rather than a list of
// names, so the exemption is a consequence of the build config instead of a
// second place to keep in step: add `windows` to a build's goos and that tool
// is immediately required on winget, winres and install.ps1 again.
func toolsBuiltForWindows(goreleaser string) (map[string]bool, error) {
	raw, err := os.ReadFile(goreleaser)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	start := strings.Index(text, "\nbuilds:\n")
	if start < 0 {
		return nil, errors.New("no builds: section in .goreleaser.yaml — fix this test rather than deleting it")
	}
	// Ends at the next top-level key, so later sections that also list tool
	// names (notarize, archives) cannot leak in.
	rest := text[start+1:]
	end := regexp.MustCompile(`(?m)^[a-z_]+:`).FindAllStringIndex(rest, 2)
	if len(end) > 1 {
		rest = rest[:end[1][0]]
	}

	mainRe := regexp.MustCompile(`main: \./cmd/([\w-]+)`)
	out := map[string]bool{}
	for _, block := range strings.Split(rest, "\n  - id:")[1:] {
		m := mainRe.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		out[m[1]] = regexp.MustCompile(`(?m)^\s+- windows$`).MatchString(block)
	}
	if len(out) == 0 {
		return nil, errors.New("no builds parsed out of .goreleaser.yaml — fix this test's patterns")
	}
	return out, nil
}

// toolsListed reads one surface's own list out of its bytes.
//
// The \r strip is not cosmetic. A Windows checkout converts these files to CRLF,
// and every `$`-anchored pattern here then fails to match — `rig` becomes
// `rig\r` before the line end. That cost this file a red Windows run, and it is
// the same trap check-winget-manifests.sh documents for winget-pkgs' own CRLF
// manifests. Matching against LF regardless of how git checked the file out is
// what keeps the rules platform-independent.
func (s surface) label() string {
	if s.what == "" {
		return s.file
	}
	return s.file + " (" + s.what + ")"
}

func (s surface) toolsListed(raw []byte) (map[string]bool, error) {
	hay := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if s.section != nil {
		var blocks []string
		for _, m := range s.section.FindAllStringSubmatch(hay, -1) {
			blocks = append(blocks, m[len(m)-1])
		}
		hay = strings.Join(blocks, "\n")
		if hay == "" {
			return nil, errors.New("the block holding the tool list no longer matches — fix this test's " +
				"section pattern, otherwise it silently stops checking anything")
		}
	}
	listed := map[string]bool{}
	for _, m := range s.list.FindAllStringSubmatch(hay, -1) {
		listed[m[1]] = true
	}
	if len(listed) == 0 {
		return nil, errors.New("no tool names extracted — fix this test's pattern rather than deleting it")
	}
	return listed, nil
}

// The rules must read the same list out of a CRLF checkout as an LF one, which
// is not obvious from a mac or Linux run: git hands Windows CRLF, and that is
// where this first broke. Converting the real files rather than a fixture keeps
// this honest as the patterns change.
func TestDistributionRulesSurviveACRLFCheckout(t *testing.T) {
	for _, s := range distSurfaces {
		raw, err := os.ReadFile(s.file)
		if err != nil {
			t.Errorf("%s: %v", s.label(), err)
			continue
		}
		lf, err := s.toolsListed(raw)
		if err != nil {
			t.Errorf("%s (LF): %v", s.label(), err)
			continue
		}
		crlf, err := s.toolsListed([]byte(toCRLF(string(raw))))
		if err != nil {
			t.Errorf("%s (CRLF): %v — a Windows checkout would read no tools here", s.label(), err)
			continue
		}
		if got, want := sortedKeys(crlf), sortedKeys(lf); !slices.Equal(got, want) {
			t.Errorf("%s: CRLF reads %v, LF reads %v", s.label(), got, want)
		}
	}
}

// toCRLF converts to CRLF from whatever the file already is. Normalising to LF
// first is the point: on Windows these files are checked out CRLF ALREADY, so a
// plain \n -> \r\n pass yields \r\r\n and tests nothing that exists. This test
// failed on Windows for exactly that reason while the code it guards was fine.
func toCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
