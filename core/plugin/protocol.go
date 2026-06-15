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
	// ecosystem's commands from the same plugin shiprig uses for releases —
	// instead of a hardcoded table. External plugins return it in their info JSON.
	DevCommands map[string][]string `json:"devCommands,omitempty"`

	// Overlays names the base ecosystem ids this adapter sits on top of. A desktop
	// framework reuses a language's manifest but owns its release: Tauri overlays
	// "cargo" (a Cargo.toml under a tauri.conf.json), Electron overlays "node" (an
	// Electron package.json). When an overlay adapter discovers a package, the base
	// adapter's package for the same directory is dropped during discovery
	// reconciliation, so the unit is owned and released once — by the overlay. Empty
	// for ordinary language adapters.
	Overlays []string `json:"overlays,omitempty"`
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
	MethodInfo        = "info"
	MethodDetect      = "detect"
	MethodDiscover    = "discover"
	MethodSetVersion  = "set-version"
	MethodPublish     = "publish"
	MethodArtifacts   = "artifacts"
	MethodReleaseInit = "release-init"
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
	APIVersion    int             `json:"apiVersion"`
	RepoRoot      string          `json:"repoRoot"`
	Package       Package         `json:"package"`
	PackageSource string          `json:"packageSource"` // feed name or URL; "nuget", "npm", ...
	Access        string          `json:"access"`        // "public" | "restricted"
	DryRun        bool            `json:"dryRun"`
	Auth          *AuthCredential `json:"auth,omitempty"`     // resolved by the engine; nil = use the ambient credential
	OIDC          bool            `json:"oidc,omitempty"`     // attempt OIDC trusted publishing (the adapter mints + exchanges the token)
	OIDCUser      string          `json:"oidcUser,omitempty"` // account/subject some registries' OIDC exchange requires (NuGet)
}

// AuthCredential is a registry credential the engine resolved (OIDC exchange,
// a 1Password/secret-manager reference, or an env token) and is handing to the
// adapter to authenticate one publish. The adapter renders its own
// ecosystem-native credentials file (npmrc / NuGet.config / cargo) from it.
// Nil Auth on a PublishRequest means "use whatever credential the package
// manager already has" — the pre-auth-seam behaviour.
//
// The token transits the plugin protocol (engine → adapter); it is never logged
// and the engine redacts it from any surfaced output.
type AuthCredential struct {
	Token      string `json:"token"`
	Method     string `json:"method,omitempty"`     // "oidc" | "secret-ref" | "env"
	Provenance bool   `json:"provenance,omitempty"` // attach provenance when the toolchain supports it
}

// PublishResponse reports the outcome.
type PublishResponse struct {
	Published bool   `json:"published"` // false if skipped (already present)
	Skipped   bool   `json:"skipped"`
	Message   string `json:"message,omitempty"`
}

// ArtifactsRequest asks an adapter to build a package's distributable artifacts
// into OutputDir and return them. This is the "produce the thing you'd ship"
// operation, kept separate from Publish (which pushes to a registry): npm pack,
// dotnet pack, cargo package, or a binary build (goreleaser). An adapter with
// nothing to build returns Skipped. Snapshot asks for an unversioned/tagless
// build (used by the rehearse flow); DryRun reports intent without building.
type ArtifactsRequest struct {
	APIVersion int     `json:"apiVersion"`
	RepoRoot   string  `json:"repoRoot"`
	Package    Package `json:"package"`
	OutputDir  string  `json:"outputDir"` // absolute dir to place built files (dist/)
	Snapshot   bool    `json:"snapshot,omitempty"`
	DryRun     bool    `json:"dryRun,omitempty"`
	// Signing carries code-signing/notarization secrets the engine resolved (via
	// the same core/auth seam as PublishRequest.Auth) for adapters that produce
	// signed installers — Tauri, Electron. Nil means build unsigned (the default
	// when no signing config is set); the engine never sets it unless signing is
	// explicitly enabled. Values are masked in any surfaced output.
	Signing *SigningCreds `json:"signing,omitempty"`
}

// SigningCreds is a resolved set of signing secrets to expose to a build as
// environment variables. The map is ENV_VAR -> secret value (already resolved
// from its op://…/env:/cmd: reference). The desktop build tools read their own
// standard variables (electron-builder's CSC_*/APPLE_*, Tauri's
// TAURI_SIGNING_PRIVATE_KEY/APPLE_*), so the engine threads them through the
// environment rather than the adapter hardcoding a per-platform matrix.
type SigningCreds struct {
	Env map[string]string `json:"env,omitempty"`
}

// Artifact kinds, for display and for deciding what gets attached to a release.
const (
	ArtifactArchive  = "archive"  // a packaged tarball/zip of binaries
	ArtifactPackage  = "package"  // a registry package file (.tgz, .nupkg, .crate)
	ArtifactBinary   = "binary"   // a bare executable
	ArtifactChecksum = "checksum" // a checksums file
)

// Artifact is one produced distributable file.
type Artifact struct {
	// Path is the absolute path to the produced file (built adapters join it onto
	// the absolute OutputDir, so it lives under OutputDir). Consumers should use it
	// as-is, not re-join it against OutputDir.
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"` // archive | package | binary | checksum
	// Attach marks an artifact that is a sensible GitHub release asset by default
	// (binaries/archives). Registry package files (.tgz/.nupkg/.crate) default to
	// false — they ship to the registry, not the release — but config may opt in.
	Attach bool `json:"attach,omitempty"`
}

// ArtifactsResponse reports the produced artifacts.
type ArtifactsResponse struct {
	Built     bool       `json:"built"` // false if skipped (nothing to build)
	Skipped   bool       `json:"skipped"`
	Message   string     `json:"message,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

// ---------------------------------------------------------------------------
// Release-init protocol — what an ecosystem needs in order to release
// ---------------------------------------------------------------------------

// ReleaseInitRequest asks an adapter to declare its release prerequisites, so a
// release `init` wizard can scaffold config and preflight the environment
// without hardcoding per-ecosystem knowledge. It is repo-aware (unlike Info) so
// an adapter can inspect the tree — e.g. notice a .goreleaser.yaml already
// exists, or template a starter from the discovered binaries.
type ReleaseInitRequest struct {
	APIVersion int       `json:"apiVersion"`
	RepoRoot   string    `json:"repoRoot"`
	Packages   []Package `json:"packages,omitempty"` // this ecosystem's discovered packages
	// OIDC reports whether OIDC trusted publishing is in play for this ecosystem
	// (config `oidc` is not "off"). An adapter that supports it can then declare
	// the token-based credential optional and emit setup guidance instead.
	OIDC bool `json:"oidc,omitempty"`
}

// ReleaseInitResponse is an adapter's declaration of its release prerequisites.
// Everything here is advisory: the wizard scaffolds files (with consent) and
// warns about gaps — it never collects secrets or publishes anything.
type ReleaseInitResponse struct {
	// Tokens are environment variables a real publish/attach will need. The
	// wizard reports which are set vs. missing; it never reads their values.
	Tokens []TokenSpec `json:"tokens,omitempty"`
	// BuildConfig is a config file the ecosystem needs in order to build
	// distributable artifacts (Go → .goreleaser.yaml). Nil when artifacts build
	// natively (npm pack, dotnet pack, cargo package) and need no extra file.
	BuildConfig *BuildConfigSpec `json:"buildConfig,omitempty"`
	// Notes are short human-readable lines the wizard surfaces (e.g. the publish
	// target), one per line.
	Notes []string `json:"notes,omitempty"`
}

// TokenSpec names an environment variable a release will need, for a preflight
// check. The wizard reports set/missing and points at where to get one; it
// never reads or stores the value.
type TokenSpec struct {
	EnvVar string `json:"envVar"`        // "GITHUB_TOKEN", "NPM_TOKEN"
	For    string `json:"for"`           // "upload release assets" — shown in the warning
	URL    string `json:"url,omitempty"` // where to obtain one (optional)
}

// BuildConfigSpec describes a config file an ecosystem needs to build its
// artifacts. The adapter generates the starter Content itself (keeping the
// knowledge in the plugin); the wizard only writes it — with consent — when
// Present is false.
type BuildConfigSpec struct {
	Path    string `json:"path"`              // ".goreleaser.yaml", relative to repo root
	Present bool   `json:"present"`           // already exists → wizard reports and skips
	Content string `json:"content,omitempty"` // starter to write when absent + confirmed
	Tool    string `json:"tool,omitempty"`    // "goreleaser" — for the prompt and a PATH preflight
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
