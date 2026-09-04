package cli

// The stack manifest describes a fused stackspace: upstream repos imported as
// prefixes of one history through josh filters (docs/STACK-DESIGN.md).
// Named stack*, not stackspace* — in this package "stackspace" already means
// intra-repo package discovery (stackspace.go).

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

// stackRepo is one upstream project fused into the stackspace under its prefix
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
	// UpstreamTag and UpstreamCommit pin this prefix to a fixed point in
	// upstream's history instead of following a branch. Either one makes `pull`
	// a no-op until the pin itself is edited, which is the whole intent: a
	// library your consumer depends on at an old release has to be fused at
	// *that* release, not at a tip whose API has moved on.
	//
	// Separate keys rather than one that guesses, because a tag and a branch can
	// share a name and git's own disambiguation order surprises people. Exactly
	// one of the three may be set.
	UpstreamTag    string `json:"upstreamTag,omitempty"`
	UpstreamCommit string `json:"upstreamCommit,omitempty"`
	// Owned marks a project as yours rather than someone else's, which changes
	// how work leaves the stackspace: `send` proposes a squashed branch to a fork,
	// `push` fast-forwards your own repo with the history intact. It cannot be
	// inferred — upstream and fork matching is suggestive, and a perfectly
	// ordinary fork arrangement looks identical — so it is stated.
	Owned bool `json:"owned,omitempty"`
	// BranchPrefix overrides the stackspace-wide prefix for this project — for the
	// upstream whose contribution guide asks for something of its own. A pointer
	// so that "" is a real answer (no prefix here) rather than "unset".
	BranchPrefix *string `json:"branchPrefix,omitempty"`
	// PublishesAs maps a package id this member produces to the id it is
	// republished under — {"Foo": "Acme.Foo"} for a fork whose patched builds go
	// to a private feed under a name that cannot collide with the public one.
	// `wire` keys redirects on the id consumers actually reference, so without
	// this an app referencing Acme.Foo would resolve it from the feed inside the
	// stackspace, build fine, and never test the fused code. The manifest is the
	// right place because it is outside every prefix: nothing here leaves in a
	// pull request. PublishPrefix is the same thing for every id the member
	// produces; an explicit map entry wins for the ids it names.
	PublishesAs   map[string]string `json:"publishesAs,omitempty"`
	PublishPrefix string            `json:"publishPrefix,omitempty"`
	// TrackBranch names a branch of Fork this prefix is IMPORTED from, instead
	// of upstream's branch. Where pull requests go does not change — Upstream
	// is still where `propose` roots its commit and `pull` follows — but a
	// stackspace rebuilt on another machine then starts from your fork's
	// branch, which is where work that has left as a proposal and not yet
	// merged actually lives. The cursor records the upstream commit that
	// branch is based on, so status and pull keep measuring against upstream.
	TrackBranch string `json:"trackBranch,omitempty"`
}

// stackPublishing is how one member's packages are known to consumers beyond
// the ids its projects declare: an explicit map, a prefix, or both.
type stackPublishing struct {
	As     map[string]string // produced id -> republished id
	Prefix string            // prepended to every produced id
}

// publishing collects the republishing rules per member, for the members that
// have any. An empty map means every package is known by the id it declares.
func (m *stackManifest) publishing() map[string]stackPublishing {
	out := map[string]stackPublishing{}
	for name, r := range m.Repos {
		if r == nil || (len(r.PublishesAs) == 0 && r.PublishPrefix == "") {
			continue
		}
		out[name] = stackPublishing{As: r.PublishesAs, Prefix: r.PublishPrefix}
	}
	return out
}

// stackPin is what a prefix tracks upstream. A branch moves and `pull` follows
// it; a tag or commit is fixed and `pull` has nothing to do until the manifest
// says otherwise.
type stackPin struct {
	Kind  string // "branch", "tag" or "commit"
	Value string
}

func (p stackPin) pinned() bool { return p.Kind != "branch" }

// describe is how a pin reads in output, kept short enough for a status column.
func (p stackPin) describe() string {
	if p.Kind == "branch" {
		return p.Value
	}
	return p.Kind + " " + p.Value
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
	// LastPin records, for a pinned prefix, which pin its cursor was resolved
	// under. Without it a tag and a repin are indistinguishable: both present as
	// "the resolved SHA differs from the cursor", so an upstream that force-moves
	// a tag would drag the stackspace along, which is the one thing a pin is for.
	// Machine-written, like LastSync, and absent for a prefix following a branch.
	LastPin map[string]string `json:"lastPin,omitempty"`
	// LastPropose maps prefix -> the branch name last given to `propose`, as
	// typed rather than as prefixed. Proposing again to the same branch is how an
	// open pull request takes review feedback, so that name is usually wanted
	// several times and is tedious to retype exactly. Machine-written, like the
	// cursors beside it.
	LastPropose map[string]string `json:"lastPropose,omitempty"`
}

func (m *stackManifest) cursor(name string) string { return m.LastSync[name] }

// ownedNames are the prefixes holding a repo of the user's own. Editing a file
// inside one is a commit to their repository, which they want; doing it inside a
// fork would put rig's plumbing into somebody else's pull request.
func (m *stackManifest) ownedNames() []string {
	var out []string
	for _, n := range m.names() {
		if r := m.Repos[n]; r != nil && r.Owned {
			out = append(out, n)
		}
	}
	return out
}

// requireRepos is the guard for verbs that act on repos. An empty manifest is a
// legitimate state — it is what `stack init` scaffolds, and what `stack add`
// writes the first entry into — so loading one is not an error; only asking it
// to do something is.
// rememberProposed records the branch a prefix was last proposed on, so the
// next one can offer it back.
func stackRememberProposed(src *cfgfind.Source, m *stackManifest, prefix, branch string) error {
	if m.LastPropose == nil {
		m.LastPropose = map[string]string{}
	}
	if m.LastPropose[prefix] == branch {
		return nil // nothing to write, and no commit to make out of nothing
	}
	m.LastPropose[prefix] = branch
	raw, err := json.Marshal(m.LastPropose)
	if err != nil {
		return err
	}
	path := []string{"lastPropose"}
	if src.Path == "" { // embedded stack block in .rig.json
		path = []string{"stack", "lastPropose"}
	}
	w := confkit.Writer{SchemaURL: stackSchemaURL}
	if !w.Set(src.File, path, string(raw)) {
		return fmt.Errorf("could not record the branch in %s", src.File)
	}
	return nil
}

func (m *stackManifest) requireRepos() error {
	if len(m.Repos) > 0 {
		return nil
	}
	// Almost always the untouched scaffold: say what to do, not what is wrong.
	return fmt.Errorf("no repos yet — `rig stack add <repo>` adds one, or fill in the manifest and run `rig stack init` again")
}

// joshVersion is the engine version this stackspace pins, or rig's default. Nil
// receiver is a real case: the version is needed to install the engine before a
// stackspace necessarily has a manifest to read.
func (m *stackManifest) joshVersion() string {
	if m != nil && m.Josh != "" {
		return m.Josh
	}
	return stackJoshVersion
}

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

// pin is what this prefix follows upstream: its tag or commit if either is set,
// otherwise the branch.
func (m *stackManifest) pin(name string) stackPin {
	if r := m.Repos[name]; r != nil {
		switch {
		case r.UpstreamTag != "":
			return stackPin{Kind: "tag", Value: r.UpstreamTag}
		case r.UpstreamCommit != "":
			return stackPin{Kind: "commit", Value: r.UpstreamCommit}
		}
	}
	return stackPin{Kind: "branch", Value: m.branch(name)}
}

// branchPrefix is what `send` prepends for this project: the repo's own setting
// if it has one, else the stackspace's, else nothing.
func (m *stackManifest) branchPrefix(name string) string {
	if r := m.Repos[name]; r != nil && r.BranchPrefix != nil {
		return *r.BranchPrefix
	}
	if m.BranchPrefix != nil {
		return *m.BranchPrefix
	}
	return stackDefaultBranchPrefix
}

// sendBranch resolves the name given to `propose` into the branch to create.
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
	if m.BranchPrefix != nil {
		if err := stackValidBranchPrefix(*m.BranchPrefix, "branchPrefix"); err != nil {
			return err
		}
	}
	for name, r := range m.Repos {
		// The key is both a josh prefix and a `HEAD:<name>` tree path, so it has
		// to name exactly one directory. Empty resolves to the stackspace root —
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
		// One upstream point per prefix. Two would mean guessing which wins, and
		// the guess would be invisible in the fused history afterwards.
		set := []string{}
		if r.UpstreamBranch != "" || r.Branch != "" {
			set = append(set, "upstreamBranch")
		}
		if r.UpstreamTag != "" {
			set = append(set, "upstreamTag")
		}
		if r.UpstreamCommit != "" {
			set = append(set, "upstreamCommit")
		}
		if len(set) > 1 {
			return fmt.Errorf("stack repo %q sets %s — a prefix follows one upstream point, so keep the branch to track it or the tag/commit to pin it",
				name, strings.Join(set, " and "))
		}
		// A republished id has to be a different name, or the entry says
		// nothing; and it cannot be empty, or every reference would match it.
		for from, to := range r.PublishesAs {
			if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
				return fmt.Errorf("stack repo %q: publishesAs maps a package id to the id it is republished under, and neither side can be empty", name)
			}
			if from == to {
				return fmt.Errorf("stack repo %q: publishesAs maps %q to itself — drop the entry, a package known by its own id needs nothing here", name, from)
			}
		}
		// A fork branch is imported on top of upstream's branch history; a
		// prefix pinned to a tag or commit has no branch for it to be based on.
		if r.TrackBranch != "" && (r.UpstreamTag != "" || r.UpstreamCommit != "") {
			return fmt.Errorf("stack repo %q sets trackBranch with a pin — a fork branch is based on upstream's branch, so keep upstreamBranch (or nothing) with it", name)
		}
		if p := r.PublishPrefix; p != "" && strings.TrimSpace(p) == "" {
			return fmt.Errorf("stack repo %q: publishPrefix is blank", name)
		}
		if c := r.UpstreamCommit; c != "" && !stackIsSHA(c) {
			return fmt.Errorf("stack repo %q has upstreamCommit %q — that must be a full 40-character commit SHA, since an abbreviation cannot be resolved without fetching the repo first",
				name, c)
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
		return fmt.Errorf("stack repo %q: the name is a directory in the stackspace, not a path", name)
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
// at the stackspace root, or a `stack` key inline in .rig.json.
func stackSpec(root string) cfgfind.Spec {
	return cfgfind.Spec{
		Label:   "stack manifest",
		Probe:   []cfgfind.DirNames{{Dir: root, Names: []string{stackFileBase}}},
		RigPath: filepath.Join(root, ".rig.json"),
		RigKeys: []string{"stack"},
	}
}

// loadStackManifest resolves and parses the manifest at root. A nil manifest with
// nil error means "not a stackspace" — callers say so themselves.
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
	if m.LastPin == nil {
		m.LastPin = map[string]string{}
	}
	// The pin this cursor was resolved under, so a later run can tell a repin
	// from a tag that moved underneath it. A prefix following a branch records
	// nothing, and drops whatever it recorded when it was pinned.
	if pin := m.pin(prefix); pin.pinned() {
		m.LastPin[prefix] = pin.describe()
	} else {
		delete(m.LastPin, prefix)
	}

	w := confkit.Writer{SchemaURL: stackSchemaURL}
	for _, kv := range []struct {
		key   string
		value map[string]string
	}{{"lastSync", m.LastSync}, {"lastPin", m.LastPin}} {
		key, value := kv.key, kv.value
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		path := []string{key}
		if src.Path == "" { // embedded key in .rig.json
			path = []string{"stack", key}
		}
		if !w.Set(src.File, path, string(raw)) {
			return fmt.Errorf("could not update %s in %s", strings.Join(path, "."), src.File)
		}
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

  // A stackspace fuses several upstream repos into this one git history,
  // each under its own directory, so a change can span them in a single commit
  // and still leave as an ordinary pull request to each project.
  //
  // Uncomment the block below, point it at your repos, then run
  //     rig stack init
  // again to import them.
  //
  // Specs are host/owner/name — no https://, no .git — because the same string
  // has to serve as a URL, an engine path, and a label.

  // Branches "rig stack propose" creates are named stack/<what-you-typed>, which
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
    //   // Your fork, where "rig stack propose" pushes PR-ready branches.
    //   // You need push access to it.
    //   "fork": "github.com/you/Some.Lib",
    //
    //   // For one of YOUR OWN projects rather than a fork of someone else's:
    //   // enables "rig stack push", which fast-forwards its own branch with
    //   // every commit that touched it, instead of squashing to one.
    //   //   "owned": true,
    //
    //   // Which branch of upstream this directory follows. Optional, main by
    //   // default. This is NOT the branch send creates — you name that one per
    //   // change:  rig stack propose some-lib fix/the-thing
    //   "upstreamBranch": "main"
    //
    //   // Instead of a branch, pin to a fixed point and pull stops following
    //   // upstream until you edit it. Use this when what depends on this
    //   // library needs an older release than upstream's tip:
    //   //   "upstreamTag": "v1.4.2"
    //   //   "upstreamCommit": "<full 40-character sha>"
    //
    //   // Import this directory from a branch of YOUR FORK instead of from
    //   // upstream — for rebuilding a stackspace elsewhere with work that has
    //   // left as a proposal and not yet merged. Pull requests still go to
    //   // upstream, and pull still follows it.
    //   //   "trackBranch": "stack/read-timeout"
    //
    //   // If you republish this fork's packages under your own id (to a
    //   // private feed, say), tell wire so a consumer referencing that id
    //   // still resolves from source here — otherwise it quietly takes the
    //   // feed's copy and the stackspace never tests the fused code.
    //   //   "publishesAs": { "Some.Lib": "You.Some.Lib" }
    //   //   "publishPrefix": "You."        // the same for every id it produces
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

// stackIsSHA reports whether s is a full commit id. Abbreviations are rejected
// where they appear: resolving one needs the object, which is the thing the pin
// exists to decide whether to fetch.
func stackIsSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
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
