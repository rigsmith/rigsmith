# Commit-based vs. changeset-based changelogs (design plan)

> Status: **implemented.** The source-adapter described below is built: a
> `versioning.source` of `"commits"` / `"both"` drives `version` and `status`,
> with commit-native changelog provenance and a commit-mode CI gate. See
> **Implementation notes** at the end for the decisions taken on the open forks.

## Context: what already exists

rigsmith's planner is **source-agnostic**. [`planner.Plan()`](../core/planner/planner.go)
consumes `[]*changeset.Changeset` + discovered packages + config, and a
`Changeset` already carries everything a commit-based mode would need:

- [`Changeset.Type` / `Breaking`](../core/changeset/changeset.go) — conventional type and `!`
- `EffectiveType()` already falls back to `ParseConventional()` on the summary
- `deriveBump()` in the planner already maps `type` + `breaking` → bump via `ChangelogGroups`

So **commit-based changelogs are not a second engine** — they're a *second
source adapter* that synthesizes in-memory `*changeset.Changeset` values from
`git log` and feeds the exact same `Plan()`. This is also how knope keeps both
modes' downstream behavior identical.

The seam is exactly one line: `changeset.Dir(ws.ChangesetDir, "")` in
[`changerig/commands/version.go`](../changerig/commands/version.go). Today that
is the only source. Commit mode swaps or augments it.

## The one genuinely hard problem: commit → package attribution

Changesets name packages explicitly in frontmatter. **Commits don't name
anything** — so the adapter must decide *which package(s) each commit bumps*.
This is the entire difficulty; everything else is plumbing. Two strategies,
mirroring knope:

1. **Path-based** (knope's default for monorepos): a commit bumps package P if
   any of its changed files live under P's directory. Both halves already exist —
   [`gitutil.ChangedFilesSince`](../core/gitutil/since.go) gives changed files,
   and discovered packages give `ManifestPath` (→ directory). A file maps to the
   *most specific* (deepest) package dir containing it. Files under no package
   (root docs, CI) attribute to nothing.
2. **Scope-based** (opt-in): use the conventional-commit scope `feat(core): …`
   → package named/aliased `core`. Needs a scope→package map in config.

Per-commit-per-package, the bump derives from the type exactly as a typed
changeset would. Breaking detection needs **both** the `!` (already handled by
`ParseConventional`) **and** a `BREAKING CHANGE:` footer in the commit body —
the footer is new parsing.

## "Since last release"

Commit mode needs `git log <last-tag>..HEAD`. You have
[`gitutil.LatestModuleVersion`](../core/gitutil/tags.go) (latest tag per module,
respecting the `core/vX.Y.Z` vs `vX.Y.Z` convention). Missing piece: a `git log`
reader returning structured commits (hash, subject, body, changed files via
`--name-only`). That's the main new gitutil function.

Subtlety: in a monorepo "the last release" is **per-package** (each module has
its own tag). The adapter computes a since-ref per package, not one global ref —
different from a single-package repo where one tag suffices.

## Proposed config surface

`Config` already has a polymorphic raw `Changelog` field and `ChangelogGroups`.
Add one top-level mode selector:

```jsonc
{
  // "changesets" (default, current behavior) | "commits" | "both"
  "versioning": { "source": "commits" },
  "changelogGroups": [ /* already drives type→section+bump */ ]
}
```

- `"both"` = union of on-disk changesets *and* commit-derived ones (knope allows
  mixing; the planner already merges multiple changesets naming the same package
  via `generateModules`).
- Scope mapping (optional):
  `"versioning": { "source": "commits", "scopes": { "core": "github.com/rigsmith/core" } }`.

## What gets built (inventory — no implementation)

1. **`core/gitutil`**: `LogSince(ctx, dir, ref) []Commit` — structured commits
   with changed files. ~1 function.
2. **New `core/commitsource` package**: `Synthesize(commits, packages, cfg)
   []*changeset.Changeset` — the attribution logic (path + scope),
   breaking-footer parsing, one synthetic changeset per (commit, package). This
   is the only real new logic. It deliberately produces *changesets*, so
   `Plan()`, the cascade, grouping, prerelease, snapshot, and the changelog
   writer all work unchanged.
3. **`config`**: decode the `versioning.source` mode + scope map.
4. **`version.go` / `status.go`**: branch at the load seam — `changeset.Dir(...)`
   vs `commitsource.Synthesize(...)` vs both. Everything after is untouched.
5. **`changerig add` semantics**: in `commits` mode there's nothing to add
   (commits *are* the source), so `add` should no-op with guidance, and the
   `require-changeset` CI action
   ([.github/actions/require-changeset](../.github/actions/require-changeset/))
   becomes a "require conventional commit" check instead.

## Decisions / known divergences from knope

- **Changelog provenance enrichment.** Today
  [`changelog.Resolve`](../core/changelog/resolver.go) finds the commit that
  *added a changeset file*. In commit mode the commit **is** the source, so
  PR/author/commit are already in hand — simpler, and arguably better
  attribution. The git/github decorators can largely be reused with a different
  input.
- **Multi-package commits.** A commit touching two packages bumps both. Fine for
  path mode; ambiguous for scope mode (single scope). Decide: scope wins, or
  scope + path union.
- **Non-conventional commits.** Knope ignores them (no bump). `deriveBump`
  currently *defaults unknown types to patch* — in commit mode you almost
  certainly want "no type → no release," or the changelog fills with
  `chore`/merge noise. A real behavior fork to settle.
- **No "unreleased changeset" preview.** A nice property of file-changesets is
  seeing pending releases before merge; commit mode loses that until a
  `version --dry-run` / `status` reads the log. Worth surfacing in `status`.

## Recommendation

Build it as a **source adapter, not a parallel pipeline** — one new
`commitsource` package + one new `gitutil.LogSince` + a config mode flag,
branching at the single load seam. That keeps the cascade / grouping /
prerelease / snapshot / changelog machinery 100% shared between both modes,
which is exactly the architecture that makes `"both"` mode nearly free.

## Implementation notes (what was built)

Built exactly as the recommendation above — a source adapter behind the single
load seam. New/changed surface:

- **[`gitutil.LogSince`](../core/gitutil/log.go)** — `(ctx, dir, ref) []Commit`,
  newest-first, each commit carrying `Hash`, `Subject`, `Body`, and the absolute
  paths it changed (`--name-only`, resolved against the repo root like
  `ChangedFilesSince`). An empty `ref` reads the whole history; an invalid ref
  errors. Records are delimited with ASCII control chars (`\x1e`/`\x1f`) so
  commit text can't collide with the parse.
- **[`core/commitsource`](../core/commitsource/commitsource.go)** —
  `Synthesize(commits, packages, repoRoot, cfg) []*changeset.Changeset`.
  Parses the conventional header (`type(scope)!: desc`), detects breaking via
  `!` **or** a `BREAKING CHANGE:`/`BREAKING-CHANGE:` footer (whose description is
  surfaced changelogen-style as a continuation line under the bullet), attributes
  the commit to package(s), and emits one synthetic changeset per commit. The
  header grammar is validated against @unjs/changelogen's parser fixtures
  ([changelogen_parity_test.go](../core/commitsource/changelogen_parity_test.go)):
  it tolerates a leading emoji / `:shortcode:` prefix (`🚀 feat: …`, `:bug: fix: …`)
  and strips a trailing `(#NN)` PR reference from the description, matching
  changelogen's `type`/`scope`/`breaking`/`description` output. The
  per-package bump is left `BumpNone` so the **planner** derives it from the type
  via `changelogGroups` — identical to a type-driven changeset file. The bullet
  text is the subject with the conventional prefix stripped (changelogen-style).
- **[`config.Versioning`](../core/config/config.go)** — decodes
  `versioning.source` (`changesets` | `commits` | `both`) and the optional
  `scopes` map; `CommitSource()`, `UsesCommits()`, `UsesChangesets()` are the
  accessors. The zero value is changeset mode, so existing repos are untouched.
- **[`Workspace.LoadChangesets`](../changerig/commands/source.go)** — the single
  seam. Reads on-disk changesets and/or synthesizes from commits per the mode.
  `version`, `status`, and `BuildPlan` all route through it; everything
  downstream is unchanged.
- **`add`** no-ops with guidance in pure `commits` mode; **`status`**'s
  missing-changeset CI gate becomes "nothing to release" in commit mode (no
  qualifying commits is not an error).

Decisions taken on the open forks:

- **Non-conventional commits → no release.** A subject that doesn't parse as
  conventional (merge commits, freeform `wip …`) produces nothing, keeping the
  changelog free of noise. A *recognized but unmapped* type still follows the
  planner's existing `deriveBump` (defaults to patch) — tune via `changelogGroups`.
- **Multi-package = path union.** A commit touching N packages bumps all N
  (deepest dir wins per file, so a file in a nested package attributes only to
  the inner one).
- **Scope wins, with path fallback.** When `scopes` maps the commit's scope to a
  *known* package, attribution is that single package; otherwise it falls back to
  path attribution. An unknown/empty scope never overrides paths.
- **Per-package since-ref.** Packages are bucketed by their last-release tag and
  each bucket is logged once; a commit only bumps a package within that
  package's own release window. Untagged packages share the empty ref (whole
  history).

### Commit-native changelog provenance

When `changelog` is `@changesets/changelog-git` or `-github`, commit-derived
entries are decorated **straight from their source commit** rather than via the
file archaeology used for on-disk changesets. The synthetic changeset carries
the full SHA ([`Changeset.Commit`](../core/changeset/changeset.go)), and
[`changelog.ResolveFromCommits`](../core/changelog/resolver.go) builds the
`CommitInfo` directly — abbreviating the hash and (for the github generator)
still resolving the PR number and author via `gh api`. `version` splits the two
populations: file changesets go through `Resolve` (find the commit that *added*
the file), commit changesets through `ResolveFromCommits` (the commit *is* the
provenance). The decorators themselves are unchanged.

### Require-conventional-commit CI gate

The [`require-changeset` action](../.github/actions/require-changeset/) gained a
`mode` input. `mode: changeset` (default) is unchanged — it diffs the PR range
for an added `.changeset/*.md`. `mode: commit` validates that the **PR title**
parses as a conventional commit, which on a squash-merge repo is exactly the
subject that lands on the base branch and drives the next release. The sticky
comment, skip-label waiver, and fork-PR handling are shared between both modes.
