package cli

// The stack manifest describes a fused workspace: upstream repos imported as
// prefixes of one history through josh filters (docs/STACK-DESIGN.md).
// Named stack*, not workspace* — in this package "workspace" already means
// intra-repo package discovery (workspace.go).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rigsmith/rigsmith/core/cfgfind"
	"github.com/rigsmith/rigsmith/core/confkit"
	"github.com/rigsmith/rigsmith/core/jsonc"
)

const (
	// stackDefaultBranchPrefix namespaces the branches `send` creates on your
	// forks, so they are recognisable among your own work and unlikely to land
	// on a name already in use there. It reserves nothing — a fork may already
	// carry stack/<name>. Set "branchPrefix" to "" to send bare names instead.
	stackDefaultBranchPrefix = "stack/"

	stackFileBase  = "rig.stack"
	stackSchemaURL = "https://rigsmith.dev/schemas/rig-stack.json"
)

// stackRepo is one upstream project fused into the workspace under its prefix
// (the manifest key). Repo specs are host/owner/name — no scheme, no .git —
// because the same spec must serve https URLs, josh proxy paths, and display.
type stackRepo struct {
	Upstream string `json:"upstream"` // canonical repo, PRs land here
	Fork     string `json:"fork"`     // contributor's fork, `send` pushes here
	// UpstreamBranch is the branch of Upstream this prefix tracks — what `pull`
	// follows and what `send` roots its commit on. Named in full because
	// "branch" alone collided with `send <repo> <branch>`, which is a *new*
	// branch on the fork: readers could not tell which one a manifest meant.
	UpstreamBranch string `json:"upstreamBranch,omitempty"` // default main
	// Branch is the name this key had before that ambiguity was worth fixing.
	// Still read, never written.
	Branch string `json:"branch,omitempty"`
	// BranchPrefix overrides the workspace-wide prefix for this project — for the
	// upstream whose contribution guide asks for something of its own. A pointer
	// so that "" is a real answer (no prefix here) rather than "unset".
	BranchPrefix *string `json:"branchPrefix,omitempty"`
}

type stackManifest struct {
	Schema string                `json:"$schema,omitempty"`
	Josh   string                `json:"josh,omitempty"` // engine version override; empty = rig's pinned default
	Repos  map[string]*stackRepo `json:"repos"`
	// BranchPrefix is prepended to the name `send` is given, so a short name per
	// change ("read-timeout") becomes the branch your forks actually carry
	// ("stack/read-timeout"). Unset means stackDefaultBranchPrefix; set it to ""
	// to send bare names. Per-repo entries override it.
	BranchPrefix *string `json:"branchPrefix,omitempty"`
	// LastSync maps prefix -> upstream SHA of its last pull: the committed
	// cursors. A separate top-level map, not a field per repo, because it is
	// machine-written — pulls rewrite this one value while the jsonc editor
	// leaves the human-authored entries (and their comments) untouched.
	LastSync map[string]string `json:"lastSync,omitempty"`
}

func (m *stackManifest) cursor(name string) string { return m.LastSync[name] }

// branch is the upstream branch a prefix tracks.
func (m *stackManifest) branch(name string) string {
	r := m.Repos[name]
	switch {
	case r == nil:
		return "main"
	case r.UpstreamBranch != "":
		return r.UpstreamBranch
	case r.Branch != "":
		return r.Branch
	}
	return "main"
}

// branchPrefix is what `send` prepends for this project: the repo's own setting
// if it has one, else the workspace's, else nothing.
func (m *stackManifest) branchPrefix(name string) string {
	if r := m.Repos[name]; r != nil && r.BranchPrefix != nil {
		return *r.BranchPrefix
	}
	if m.BranchPrefix != nil {
		return *m.BranchPrefix
	}
	return stackDefaultBranchPrefix
}

// sendBranch resolves the name given to `send` into the branch to create.
// A name that already carries the prefix is left alone, so re-sending by
// pasting the full branch name back in doesn't stutter it.
func (m *stackManifest) sendBranch(name, given string) string {
	prefix := m.branchPrefix(name)
	if prefix == "" || strings.HasPrefix(given, prefix) {
		return given
	}
	return prefix + given
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
		// Almost always the untouched scaffold: say what to do, not what is wrong.
		return fmt.Errorf("no repos yet — uncomment the example entry and point it at your upstream and fork, then run `rig stack init` again")
	}
	if m.BranchPrefix != nil {
		if err := stackValidBranchPrefix(*m.BranchPrefix, "branchPrefix"); err != nil {
			return err
		}
	}
	for name, r := range m.Repos {
		// The key is both a josh prefix and a `HEAD:<name>` tree path, so it has
		// to name exactly one directory. Empty resolves to the workspace root —
		// `send` would push the whole fused tree to one upstream.
		if err := stackValidPrefix(name); err != nil {
			return err
		}
		if r == nil || r.Upstream == "" || r.Fork == "" {
			return fmt.Errorf("stack repo %q needs both upstream and fork", name)
		}
		// Both spellings set to different branches is a manifest whose author
		// believed one of them meant something else; refuse rather than pick.
		if r.BranchPrefix != nil {
			if err := stackValidBranchPrefix(*r.BranchPrefix, "stack repo "+name); err != nil {
				return err
			}
		}
		if r.UpstreamBranch != "" && r.Branch != "" && r.UpstreamBranch != r.Branch {
			return fmt.Errorf("stack repo %q sets upstreamBranch %q and branch %q — keep upstreamBranch and drop branch, which is the old name for it",
				name, r.UpstreamBranch, r.Branch)
		}
		for _, spec := range []string{r.Upstream, r.Fork} {
			// A paste is normalised into host/owner/name before this runs, so
			// anything still carrying a scheme was not a shape we recognise.
			if strings.Contains(spec, "://") {
				return fmt.Errorf("stack repo %q: %q is not a repository URL this understands — host/owner/name, or the https or ssh URL of one", name, spec)
			}
			// Counting separators would accept "github.com//repo": require three
			// non-empty components, or the URL fails later as an opaque git error.
			if parts := strings.Split(spec, "/"); len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
				return fmt.Errorf("stack repo %q: %q must be host/owner/name", name, spec)
			}
		}
	}
	return nil
}

// stackValidBranchPrefix keeps a prefix to something git will accept once a
// change name is appended to it. It deliberately allows a trailing slash
// ("stack/") and a bare lead-in ("jc-") alike.
func stackValidBranchPrefix(prefix, where string) error {
	if prefix == "" {
		return nil
	}
	bad := func(why string) error {
		return fmt.Errorf("%s: branch prefix %q %s", where, prefix, why)
	}
	switch {
	case strings.HasPrefix(prefix, "/"), strings.HasPrefix(prefix, "-"):
		return bad("cannot start with " + strconv.Quote(prefix[:1]))
	case strings.ContainsAny(prefix, " \t~^:?*[\\"):
		return bad("contains a character git will not accept in a branch name")
	case strings.Contains(prefix, ".."), strings.Contains(prefix, "@{"):
		return bad("contains a sequence git will not accept in a branch name")
	}
	// git refuses a path component that is empty, starts with a dot, or ends
	// with .lock. A trailing slash leaves an empty last component here, and that
	// one is fine: the change name lands in it.
	parts := strings.Split(prefix, "/")
	for i, part := range parts {
		last := i == len(parts)-1
		switch {
		case part == "" && !last:
			return bad("has an empty path segment")
		case strings.HasPrefix(part, "."):
			return bad("has a path segment starting with a dot")
		case strings.HasSuffix(part, ".lock"):
			return bad("has a path segment ending in .lock")
		}
	}
	return nil
}

// stackValidPrefix rejects the keys that would escape their own directory.
func stackValidPrefix(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("stack manifest has a repo with an empty name")
	case name == "." || name == "..":
		return fmt.Errorf("stack repo %q: the name is a directory in the workspace, not a path", name)
	case strings.EqualFold(name, ".git"):
		return fmt.Errorf("stack repo %q: git reserves that name, and the import would be rejected", name)
	case strings.ContainsAny(name, "/\\"), strings.ContainsAny(name, " \t"):
		return fmt.Errorf("stack repo %q: the name must be a single directory, without separators or spaces", name)
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("stack repo %q: the name must not start with a dash", name)
	}
	return nil
}

// stackSpec is the cfgfind spec for the stack manifest: a dedicated rig.stack.jsonc/.json
// at the workspace root, or a `stack` key inline in .rig.json.
func stackSpec(root string) cfgfind.Spec {
	return cfgfind.Spec{
		Label:   "stack manifest",
		Probe:   []cfgfind.DirNames{{Dir: root, Names: []string{stackFileBase}}},
		RigPath: filepath.Join(root, ".rig.json"),
		RigKeys: []string{"stack"},
	}
}

// loadStackManifest resolves and parses the manifest at root. A nil manifest with
// nil error means "not a stack workspace" — callers say so themselves.
func loadStackManifest(root string) (*stackManifest, *cfgfind.Source, error) {
	src, err := cfgfind.Find(stackSpec(root))
	if err != nil || src == nil {
		return nil, nil, err
	}
	var m stackManifest
	if err := jsonc.Unmarshal(src.Data, &m); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", src.Origin, err)
	}
	m.normalize()
	if err := m.validate(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", src.Origin, err)
	}
	return &m, src, nil
}

// stackSetCursor records a pull's upstream SHA: the whole lastSync map is
// rewritten as one value (depth ≤2, within the comment-preserving editor's
// reach for both a dedicated file and an inline `stack` key), everything else in
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

// stackRemoteURL turns a host/owner/name spec into a fetchable URL. Loopback
// hosts get http: a test server on 127.0.0.1 cannot hold a certificate anyone
// would trust, and this is the only way the verbs are exercisable end to end
// without a forge.
func stackRemoteURL(spec string) string {
	host, rest, _ := strings.Cut(spec, "/")
	return stackRemoteScheme(host) + stackHostForURL(host) + "/" + rest + ".git"
}

// stackRemoteScheme is https everywhere but loopback.
func stackRemoteScheme(host string) string {
	switch h, _ := stackHostPort(host); h {
	case "127.0.0.1", "localhost", "::1":
		return "http://"
	}
	return "https://"
}

// stackHostForURL brackets a bare IPv6 literal, which is only legal in a URL
// inside brackets. An already-bracketed host is left as it is.
func stackHostForURL(host string) string {
	if strings.HasPrefix(host, "[") || !strings.Contains(host, "::") {
		return host
	}
	return "[" + host + "]"
}

// stackHostPort splits a host spec into host and port. Cutting at the first
// colon would be wrong for IPv6, where the address itself carries colons:
// "::1" would come back as an empty host and never match the loopback list.
func stackHostPort(host string) (string, string) {
	if strings.HasPrefix(host, "[") {
		h, rest, _ := strings.Cut(strings.TrimPrefix(host, "["), "]")
		return h, strings.TrimPrefix(rest, ":")
	}
	if strings.Count(host, ":") == 1 {
		h, p, _ := strings.Cut(host, ":")
		return h, p
	}
	return host, "" // bare IPv6, or a host with no port
}

// stackSplitHost splits host/owner/name into the proxy's --remote host and the
// owner/name path josh expects in the URL.
func stackSplitHost(spec string) (host, path string) {
	host, path, _ = strings.Cut(spec, "/")
	return host, path
}

// stackManifestTemplate is what `stack init` writes when no manifest exists yet.
// The example entry is commented out deliberately: an active one would send the
// next `init` chasing a repo that does not exist, and the error for an empty
// repos block says what to do instead.
const stackManifestTemplate = `{
  "$schema": "` + stackSchemaURL + `",

  // A stack workspace fuses several upstream repos into this one git history,
  // each under its own directory, so a change can span them in a single commit
  // and still leave as an ordinary pull request to each project.
  //
  // Uncomment the block below, point it at your repos, then run
  //     rig stack init
  // again to import them.
  //
  // Specs are host/owner/name — no https://, no .git — because the same string
  // has to serve as a URL, an engine path, and a label.

  // Branches "rig stack send" creates are named stack/<what-you-typed>, which
  // keeps them recognisable on a fork that also carries your own work. Override
  // it here, or set it to "" to send bare names. A repo may override it too.
  // "branchPrefix": "stack/",

  "repos": {
    // The key is the directory this project is fused under, and the name you
    // pass to the verbs:  rig stack pull some-lib

    // "some-lib": {
    //   // Where pull requests eventually go. rig only ever reads from it.
    //   "upstream": "github.com/them/Some.Lib",
    //
    //   // Your fork, where "rig stack send" pushes PR-ready branches.
    //   // You need push access to it.
    //   "fork": "github.com/you/Some.Lib",
    //
    //   // Which branch of upstream this directory follows. Optional, main by
    //   // default. This is NOT the branch send creates — you name that one per
    //   // change:  rig stack send some-lib fix/the-thing
    //   "upstreamBranch": "main"
    // },

    // "another-lib": { "upstream": "...", "fork": "..." }
  }

  // A "lastSync" block appears here after the first import, recording the
  // upstream commit each directory was taken from. Written by rig, not by hand.
}
`

func stackWriteTemplate(root string) (string, error) {
	p := filepath.Join(root, stackFileBase+".jsonc")
	if _, err := os.Stat(p); err == nil {
		return p, fmt.Errorf("%s already exists", p)
	}
	return p, os.WriteFile(p, []byte(stackManifestTemplate), 0o644)
}

// stackNormalizeSpec reduces what people actually paste — the URL in the browser
// bar, the one the clone button hands out, an ssh remote — to the canonical
// host/owner/name. Rejecting those was pure friction: the information is all
// there, in a form the user did not choose.
//
// Anything unrecognised comes back unchanged so that validate reports the
// specific problem, rather than this quietly reshaping it into a different one.
func stackNormalizeSpec(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	// A URL copied from a browser can carry a query or a fragment. Neither is
	// part of the repository's identity, and leaving one attached also defeats
	// the .git trim below, so the spec would keep a suffix and gain another.
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if _, rest, ok := strings.Cut(s, "://"); ok {
		s = rest
	} else if at := strings.Index(s, "@"); at >= 0 {
		// scp-style git@host:owner/name, where a colon separates host from path
		// and is the only thing distinguishing it from a host carrying a port.
		// An IPv6 literal brings its own colons, so only one after the closing
		// bracket can be the separator.
		host := s[at+1:]
		sep := strings.Index(host, ":")
		if strings.HasPrefix(host, "[") {
			sep = -1
			if end := strings.Index(host, "]"); end >= 0 {
				if c := strings.Index(host[end:], ":"); c >= 0 {
					sep = end + c
				}
			}
		}
		if sep >= 0 {
			s = host[:sep] + "/" + host[sep+1:]
		}
	}
	// Userinfo left over from an ssh:// URL. Bounded to the first path segment
	// so an "@" inside a repository name is not mistaken for one.
	if slash := strings.Index(s, "/"); slash >= 0 {
		if at := strings.LastIndex(s[:slash], "@"); at >= 0 {
			s = s[at+1:]
		}
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	return strings.TrimSuffix(s, "/")
}

// normalize canonicalises every repo spec before validation, so the rest of the
// tool only ever sees host/owner/name.
func (m *stackManifest) normalize() {
	for _, r := range m.Repos {
		if r == nil {
			continue
		}
		r.Upstream = stackNormalizeSpec(r.Upstream)
		r.Fork = stackNormalizeSpec(r.Fork)
	}
}
