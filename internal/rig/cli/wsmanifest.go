package cli

// The ws manifest describes a fused workspace: upstream repos imported as
// prefixes of one history through josh filters (docs/WORKSPACE-DESIGN.md).
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
	wsFileBase  = "rig.ws"
	wsSchemaURL = "https://rigsmith.dev/schemas/rig-ws.json"
)

// wsRepo is one upstream project fused into the workspace under its prefix
// (the manifest key). Repo specs are host/owner/name — no scheme, no .git —
// because the same spec must serve https URLs, josh proxy paths, and display.
type wsRepo struct {
	Upstream string `json:"upstream"`         // canonical repo, PRs land here
	Fork     string `json:"fork"`             // contributor's fork, `send` pushes here
	Branch   string `json:"branch,omitempty"` // upstream branch to track (default main)
}

type wsManifest struct {
	Schema string             `json:"$schema,omitempty"`
	Josh   string             `json:"josh,omitempty"` // engine version override; empty = rig's pinned default
	Repos  map[string]*wsRepo `json:"repos"`
	// LastSync maps prefix -> upstream SHA of its last pull: the committed
	// cursors. A separate top-level map, not a field per repo, because it is
	// machine-written — pulls rewrite this one value while the jsonc editor
	// leaves the human-authored entries (and their comments) untouched.
	LastSync map[string]string `json:"lastSync,omitempty"`
}

func (m *wsManifest) cursor(name string) string { return m.LastSync[name] }

func (m *wsManifest) branch(name string) string {
	if r := m.Repos[name]; r != nil && r.Branch != "" {
		return r.Branch
	}
	return "main"
}

// names returns the prefixes in stable order, so multi-repo verbs and their
// output are deterministic.
func (m *wsManifest) names() []string {
	out := make([]string, 0, len(m.Repos))
	for n := range m.Repos {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (m *wsManifest) validate() error {
	if len(m.Repos) == 0 {
		return fmt.Errorf("ws manifest has no repos")
	}
	for name, r := range m.Repos {
		if r == nil || r.Upstream == "" || r.Fork == "" {
			return fmt.Errorf("ws repo %q needs both upstream and fork", name)
		}
		for _, spec := range []string{r.Upstream, r.Fork} {
			if strings.Contains(spec, "://") || strings.HasSuffix(spec, ".git") {
				return fmt.Errorf("ws repo %q: %q must be host/owner/name (no scheme, no .git)", name, spec)
			}
			if strings.Count(spec, "/") != 2 {
				return fmt.Errorf("ws repo %q: %q must be host/owner/name", name, spec)
			}
		}
	}
	return nil
}

// wsSpec is the cfgfind spec for the ws manifest: a dedicated rig.ws.jsonc/.json
// at the workspace root, or a `ws` key inline in .rig.json.
func wsSpec(root string) cfgfind.Spec {
	return cfgfind.Spec{
		Label:   "ws manifest",
		Probe:   []cfgfind.DirNames{{Dir: root, Names: []string{wsFileBase}}},
		RigPath: filepath.Join(root, ".rig.json"),
		RigKeys: []string{"ws"},
	}
}

// loadWsManifest resolves and parses the manifest at root. A nil manifest with
// nil error means "not a ws workspace" — callers say so themselves.
func loadWsManifest(root string) (*wsManifest, *cfgfind.Source, error) {
	src, err := cfgfind.Find(wsSpec(root))
	if err != nil || src == nil {
		return nil, nil, err
	}
	var m wsManifest
	if err := jsonc.Unmarshal(src.Data, &m); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", src.Origin, err)
	}
	if err := m.validate(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", src.Origin, err)
	}
	return &m, src, nil
}

// wsSetCursor records a pull's upstream SHA: the whole lastSync map is
// rewritten as one value (depth ≤2, within the comment-preserving editor's
// reach for both a dedicated file and an inline `ws` key), everything else in
// the file stays byte-for-byte.
func wsSetCursor(src *cfgfind.Source, m *wsManifest, prefix, sha string) error {
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
		path = []string{"ws", "lastSync"}
	}
	w := confkit.Writer{SchemaURL: wsSchemaURL}
	if !w.Set(src.File, path, string(raw)) {
		return fmt.Errorf("could not update %s in %s", strings.Join(path, "."), src.File)
	}
	return nil
}

// wsHTTPSURL turns a host/owner/name spec into a fetchable https URL.
func wsHTTPSURL(spec string) string { return "https://" + spec + ".git" }

// wsSplitHost splits host/owner/name into the proxy's --remote host and the
// owner/name path josh expects in the URL.
func wsSplitHost(spec string) (host, path string) {
	host, path, _ = strings.Cut(spec, "/")
	return host, path
}

// wsManifestTemplate is what `ws init` writes when no manifest exists yet —
// a commented skeleton, because the URLs are facts only the user knows.
const wsManifestTemplate = `{
  "$schema": "` + wsSchemaURL + `",
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

func wsWriteTemplate(root string) (string, error) {
	p := filepath.Join(root, wsFileBase+".jsonc")
	if _, err := os.Stat(p); err == nil {
		return p, fmt.Errorf("%s already exists", p)
	}
	return p, os.WriteFile(p, []byte(wsManifestTemplate), 0o644)
}
