// Package plugin defines rigsmith's extension contract. Both ecosystem/language
// adapters and changelog generators are pluggable through one mechanism:
// delegate to an external command over a versioned JSON-on-stdin / result-on-
// stdout protocol — the same delegation model the tool already uses for git,
// gh, and the native package managers.
//
// Crucially, the built-in adapters are NOT a privileged bypass: they implement
// the very same Go interfaces the subprocess transport mirrors (Ecosystem,
// ChangelogGenerator). The built-ins are the reference implementations of the
// contract, which keeps the contract honest (a built-in golden that can't be
// reproduced from the request object means the contract is missing a field).
//
// Rejected alternatives (see net-changesets docs/changelog-generator-plugins-
// design.md): Go plugin .so, HashiCorp go-plugin gRPC, WASM. A plugin here is a
// stateless one-shot pure function — the ideal shape for a subprocess.
package plugin

import "encoding/json"

// APIVersion is the highest protocol version this build speaks. The engine
// sends this; a plugin that doesn't recognize it must exit non-zero rather than
// guess. Additive fields don't bump it; removing/renaming/re-meaning a field does.
const APIVersion = 1

// ---------------------------------------------------------------------------
// Shared package model
// ---------------------------------------------------------------------------

// Package is one releasable unit discovered by an ecosystem adapter. It is the
// generalization of net-changesets' CsProject + ModuleChangelog identity, made
// ecosystem-agnostic (the JS IPackage, the .csproj, the Cargo crate, etc.).
type Package struct {
	// Name is the identity used by changesets (frontmatter names match this).
	Name string `json:"name"`
	// DisplayName is the human title for the changelog header; defaults to Name.
	DisplayName string `json:"displayName,omitempty"`
	// Version is the current version as written in the manifest.
	Version string `json:"version"`
	// Dir is the package directory, relative to the repo root.
	Dir string `json:"dir"`
	// ManifestPath is the file holding the package metadata (.csproj, package.json, go.mod...).
	ManifestPath string `json:"manifestPath"`
	// VersionFile is the file that actually holds the version — may differ from
	// ManifestPath when the version is shared (Directory.Build.props, workspace
	// root). Empty means "same as ManifestPath".
	VersionFile string `json:"versionFile,omitempty"`
	// Private packages are versioned but never published.
	Private bool `json:"private,omitempty"`
	// Dependencies are this package's intra-repo dependencies, used by the cascade.
	Dependencies []Dependency `json:"dependencies,omitempty"`
}

// DependencyKind distinguishes normal/dev/peer dependencies, which the cascade
// treats differently (peer handling follows @changesets semantics).
type DependencyKind string

const (
	DepNormal DependencyKind = "normal"
	DepDev    DependencyKind = "dev"
	DepPeer   DependencyKind = "peer"
)

// Dependency is an intra-repo dependency edge.
type Dependency struct {
	// Name is the depended-on package's Name.
	Name string `json:"name"`
	// Kind is normal/dev/peer.
	Kind DependencyKind `json:"kind,omitempty"`
	// Range is the version constraint as written (e.g. "^1.2.0", "workspace:*").
	// Empty for ecosystems without ranges (e.g. .NET ProjectReference) — which
	// the cascade treats as always-out-of-range, hence always-patch-bump.
	Range string `json:"range,omitempty"`
}

// HasRange reports whether the dependency carries a comparable version range.
func (d Dependency) HasRange() bool { return d.Range != "" }

// ---------------------------------------------------------------------------
// Ecosystem protocol — request/response envelopes
// ---------------------------------------------------------------------------

// EcosystemInfo is returned by the "info" method: identity and capabilities.
type EcosystemInfo struct {
	APIVersion       int      `json:"apiVersion"`
	ID               string   `json:"id"`               // "dotnet", "node", "go"
	DisplayName      string   `json:"displayName"`      // ".NET", "Node", "Go"
	Capabilities     []string `json:"capabilities"`     // subset of MethodDiscover, MethodSetVersion, MethodPublish, ...
	ManifestPatterns []string `json:"manifestPatterns"` // globs that signal this ecosystem (*.csproj, package.json, go.mod)

	// DevCommands maps a dev-loop verb (build/test/run/format/lint/typecheck) to
	// the native argv that runs it. This is how the `rig` dev launcher learns an
	// ecosystem's commands from the same plugin relrig uses for releases —
	// instead of a hardcoded table. External plugins return it in their info JSON.
	DevCommands map[string][]string `json:"devCommands,omitempty"`
}

// Dev-loop and maintenance verb keys for EcosystemInfo.DevCommands. The dev-loop
// verbs run the everyday loop; the maintenance/package verbs map to the
// ecosystem's native package-management commands (rig appends any package args).
const (
	VerbBuild     = "build"
	VerbTest      = "test"
	VerbRun       = "run"
	VerbFormat    = "format"
	VerbLint      = "lint"
	VerbTypecheck = "typecheck"
	VerbCoverage  = "coverage"

	VerbInstall   = "install"   // restore/install all deps
	VerbCI        = "ci"        // frozen/clean install
	VerbAdd       = "add"       // add a dependency (args appended)
	VerbUninstall = "uninstall" // remove a dependency (args appended)
	VerbOutdated  = "outdated"  // list outdated deps
	VerbUpgrade   = "upgrade"   // upgrade deps
	VerbClean     = "clean"     // remove build outputs
	VerbGlobal    = "global"    // install a global tool (args appended)
	VerbDlx       = "dlx"       // run a tool once without installing (args appended)
)

// Method names for the ecosystem protocol (the plugin's first argv).
const (
	MethodInfo       = "info"
	MethodDetect     = "detect"
	MethodDiscover   = "discover"
	MethodSetVersion = "set-version"
	MethodPublish    = "publish"
)

// DiscoverRequest asks an adapter to enumerate the packages it owns.
type DiscoverRequest struct {
	APIVersion int    `json:"apiVersion"`
	RepoRoot   string `json:"repoRoot"`
	SourcePath string `json:"sourcePath"` // relative root to scan; "." by default
}

// DiscoverResponse returns the discovered packages.
type DiscoverResponse struct {
	Packages []Package `json:"packages"`
}

// SetVersionRequest asks an adapter to stamp a new version into a package's
// manifest (format-preserving). The engine resolves the final version string
// (including any prerelease/snapshot suffix) before calling.
type SetVersionRequest struct {
	APIVersion int     `json:"apiVersion"`
	RepoRoot   string  `json:"repoRoot"`
	Package    Package `json:"package"`
	NewVersion string  `json:"newVersion"`
	// DependencyUpdates are intra-repo dependency ranges to rewrite in the same
	// manifest (e.g. bump "^1.2.0" -> "^1.3.0"). Empty for ecosystems where the
	// version reference carries no range (.NET ProjectReference).
	DependencyUpdates []DependencyUpdate `json:"dependencyUpdates,omitempty"`
}

// DependencyUpdate is a single dependency-range rewrite.
type DependencyUpdate struct {
	Name       string `json:"name"`
	NewVersion string `json:"newVersion"`
}

// PublishRequest asks an adapter to publish a package via its native package
// manager (dotnet nuget push, npm publish, ...). Adapters are expected to be
// idempotent: query the registry and skip versions that already exist.
type PublishRequest struct {
	APIVersion    int     `json:"apiVersion"`
	RepoRoot      string  `json:"repoRoot"`
	Package       Package `json:"package"`
	PackageSource string  `json:"packageSource"` // feed name or URL; "nuget", "npm", ...
	Access        string  `json:"access"`        // "public" | "restricted"
	DryRun        bool    `json:"dryRun"`
}

// PublishResponse reports the outcome.
type PublishResponse struct {
	Published bool   `json:"published"` // false if skipped (already present)
	Skipped   bool   `json:"skipped"`
	Message   string `json:"message,omitempty"`
}

// ---------------------------------------------------------------------------
// Changelog generator protocol (per net-changesets plugin design doc)
// ---------------------------------------------------------------------------

// ChangelogRequest is serialized to a changelog generator's stdin, once per
// package being released. The generator returns the rendered release entry
// (the block under the package's "# Title", excluding the title) on stdout.
type ChangelogRequest struct {
	APIVersion        int                `json:"apiVersion"`
	Package           ChangelogPackage   `json:"package"`
	Bump              string             `json:"bump"` // major | minor | patch
	Changes           []ChangelogChange  `json:"changes"`
	DependencyUpdates []DependencyUpdate `json:"dependencyUpdates,omitempty"`
	Context           ChangelogContext   `json:"context"`
	// Contributors are the release's authors, already de-duplicated, filtered by
	// the configured exclude list, and sorted. Empty unless the `contributors`
	// config is enabled. The builtin generator renders them as a trailing section.
	Contributors []Author `json:"contributors,omitempty"`
	// ContributorsSection overrides the contributors heading; empty renders the
	// default "❤️ Contributors".
	ContributorsSection string `json:"contributorsSection,omitempty"`
}

// Author identifies a changelog contributor. Email is carried for de-duplication
// and exclude-matching only — it is never rendered. Login is the GitHub handle
// when it could be resolved (used to link to the author's GitHub page).
type Author struct {
	Name  string `json:"name"`
	Login string `json:"login,omitempty"`
	Email string `json:"email,omitempty"`
}

// ChangelogPackage is the package facet relevant to changelog rendering.
type ChangelogPackage struct {
	Name           string `json:"name"`
	DisplayName    string `json:"displayName"`
	CurrentVersion string `json:"currentVersion"`
	NewVersion     string `json:"newVersion"` // already includes prerelease/snapshot suffix
}

// ChangelogChange is one entry's worth of summary plus resolved enrichment.
type ChangelogChange struct {
	Bump    string `json:"bump"`
	Summary string `json:"summary"`
	// Type is the conventional-commit type (feat/fix/perf/…) when the changeset
	// declared or implied one; lets a generator group by real type rather than
	// just bump. Empty when untyped.
	Type string `json:"type,omitempty"`
	// Breaking marks a breaking change (a `!` on the type).
	Breaking bool   `json:"breaking,omitempty"`
	Commit   string `json:"commit,omitempty"`
	PR       int    `json:"pr,omitempty"`
	Author   string `json:"author,omitempty"`
}

// ChangelogContext mirrors the release-command context where meaningful.
type ChangelogContext struct {
	Tag      string `json:"tag"`
	RepoRoot string `json:"repoRoot"`
}

// rawMessage is a tiny alias to keep call sites tidy.
type rawMessage = json.RawMessage
