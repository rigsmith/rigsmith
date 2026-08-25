package cli

// The stack manifest describes a fused workspace: upstream repos imported as
// prefixes of one history through josh filters (docs/STACK-DESIGN.md).
// Named ws*, not workspace* — in this package "workspace" already means
// intra-repo package discovery (workspace.go).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/cfgfind"
	"github.com/rigsmith/rigsmith/core/confkit"
	"github.com/rigsmith/rigsmith/core/jsonc"
)

const (
	stackFileBase  = "rig.stack"
	stackSchemaURL = "https://rigsmith.dev/schemas/rig-stack.json"
)

// stackRepo is one upstream project fused into the workspace under its prefix
// (the manifest key). Repo specs are host/owner/name — no scheme, no .git —
// because the same spec must serve https URLs, josh proxy paths, and display.
type stackRepo struct {
	Upstream string `json:"upstream"`         // canonical repo, PRs land here
	Fork     string `json:"fork"`             // contributor's fork, `send` pushes here
	Branch   string `json:"branch,omitempty"` // upstream branch to track (default main)
}

type stackManifest struct {
	Schema string                `json:"$schema,omitempty"`
	Josh   string                `json:"josh,omitempty"` // engine version override; empty = rig's pinned default
	Repos  map[string]*stackRepo `json:"repos"`
	// LastSync maps prefix -> upstream SHA of its last pull: the committed
	// cursors. A separate top-level map, not a field per repo, because it is
	// machine-written — pulls rewrite this one value while the jsonc editor
	// leaves the human-authored entries (and their comments) untouched.
	LastSync map[string]string `json:"lastSync,omitempty"`
}

func (m *stackManifest) cursor(name string) string { return m.LastSync[name] }

func (m *stackManifest) branch(name string) string {
	if r := m.Repos[name]; r != nil && r.Branch != "" {
		return r.Branch
	}
	return "main"
}

// names returns the prefixes in stable order, so multi-repo verbs and their
// output are deterministic.
func (m *stackManifest) names() []string {
	out := make([]string, 0, len(m.Repos))
	for n := range m.Repos {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (m *stackManifest) validate() error {
	if len(m.Repos) == 0 {
		return fmt.Errorf("stack manifest has no repos")
	}
	for name, r := range m.Repos {
		if r == nil || r.Upstream == "" || r.Fork == "" {
			return fmt.Errorf("stack repo %q needs both upstream and fork", name)
		}
		for _, spec := range []string{r.Upstream, r.Fork} {
			if strings.Contains(spec, "://") || strings.HasSuffix(spec, ".git") {
				return fmt.Errorf("stack repo %q: %q must be host/owner/name (no scheme, no .git)", name, spec)
			}
			if strings.Count(spec, "/") != 2 {
				return fmt.Errorf("stack repo %q: %q must be host/owner/name", name, spec)
			}
		}
	}
	return nil
}

// stackSpec is the cfgfind spec for the stack manifest: a dedicated rig.stack.jsonc/.json
// at the workspace root, or a `ws` key inline in .rig.json.
func stackSpec(root string) cfgfind.Spec {
	return cfgfind.Spec{
		Label:   "stack manifest",
		Probe:   []cfgfind.DirNames{{Dir: root, Names: []string{stackFileBase}}},
		RigPath: filepath.Join(root, ".rig.json"),
		RigKeys: []string{"stack"},
	}
}

// loadWsManifest resolves and parses the manifest at root. A nil manifest with
// nil error means "not a stack workspace" — callers say so themselves.
func loadWsManifest(root string) (*stackManifest, *cfgfind.Source, error) {
	src, err := cfgfind.Find(stackSpec(root))
	if err != nil || src == nil {
		return nil, nil, err
	}
	var m stackManifest
	if err := jsonc.Unmarshal(src.Data, &m); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", src.Origin, err)
	}
	if err := m.validate(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", src.Origin, err)
	}
	return &m, src, nil
}

// stackSetCursor records a pull's upstream SHA: the whole lastSync map is
// rewritten as one value (depth ≤2, within the comment-preserving editor's
// reach for both a dedicated file and an inline `ws` key), everything else in
// the file stays byte-for-byte.
func stackSetCursor(src *cfgfind.Source, m *stackManifest, prefix, sha string) error {
	if m.LastSync == nil {
		m.LastSync = map[string]string{}
	}
	m.LastSync[prefix] = sha
	raw, err := json.Marshal(m.LastSync)
	if err != nil {
		return err
	}
	path := []string{"lastSync"}
	if src.Path == "" { // embedded key in .rig.json
		path = []string{"stack", "lastSync"}
	}
	w := confkit.Writer{SchemaURL: stackSchemaURL}
	if !w.Set(src.File, path, string(raw)) {
		return fmt.Errorf("could not update %s in %s", strings.Join(path, "."), src.File)
	}
	return nil
}

// stackHTTPSURL turns a host/owner/name spec into a fetchable https URL.
func stackHTTPSURL(spec string) string { return "https://" + spec + ".git" }

// stackSplitHost splits host/owner/name into the proxy's --remote host and the
// owner/name path josh expects in the URL.
func stackSplitHost(spec string) (host, path string) {
	host, path, _ = strings.Cut(spec, "/")
	return host, path
}

// stackManifestTemplate is what `stack init` writes when no manifest exists yet —
// a commented skeleton, because the URLs are facts only the user knows.
const stackManifestTemplate = `{
  "$schema": "` + stackSchemaURL + `",
  // One entry per upstream project; the key is the prefix directory the
  // project's history is fused under. Specs are host/owner/name.
  "repos": {
    // "some-lib": {
    //   "upstream": "github.com/them/Some.Lib",
    //   "fork":     "github.com/you/Some.Lib",
    //   "branch":   "main"
    // }
  }
}
`

func stackWriteTemplate(root string) (string, error) {
	p := filepath.Join(root, stackFileBase+".jsonc")
	if _, err := os.Stat(p); err == nil {
		return p, fmt.Errorf("%s already exists", p)
	}
	return p, os.WriteFile(p, []byte(stackManifestTemplate), 0o644)
}
