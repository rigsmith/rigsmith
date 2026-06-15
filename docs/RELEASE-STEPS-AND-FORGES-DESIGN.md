# Release steps, multi-forge & issue trackers — design

> Status: **DESIGN / agreed scope** (2026-06-14). Drives implementation; build in
> the slices at the end. Composes with
> [RELEASE-PIPELINE-DESIGN.md](RELEASE-PIPELINE-DESIGN.md) (the `artifacts` build
> step + `--rehearse`): that doc owns the binary-build step; **this** doc owns the
> step *shape* (names + order) and the forge generalization. The canonical order
> below supersedes the `[…, githubRelease, artifacts]` order there — same
> `artifacts` step, now after a renamed `release` and a new `tag` step.

## Goals

1. **A legible step model** — every release concern is its own named, reorderable,
   toggleable step. Promote `tag` out of `publish` (where it hides today) and
   rename `githubRelease` → `release`. Canonical order:

   ```
   version → commit → publish → tag → push → release → artifacts
   ```

2. **Multi-forge releases** — GitHub, GitLab, **and Gitea** releases first-class,
   for parity with knope / release-it. The forge step is GitHub-only today (via
   `gh`).

3. **Issue-tracker integration** (roadmap) — on release, comment on / close issues
   referenced by the released changes, across GitHub / Gitea / Jira. Nothing like
   this exists today.

## Current state (grounding)

- `DefaultOrder = [version, commit, publish, push, githubRelease]`
  (`internal/shiprig/pipeline/resolve.go`).
- **Tags are not a step.** They're created *inside* `publish`
  (`internal/shiprig/cli/publish.go` → `gitutil.CreateTag`/`PushTag`), and
  `git push --follow-tags` (the `push` step) is the backstop. A standalone
  `shiprig tag` command exists (`internal/shiprig/cli/tag.go`) but isn't wired
  into the pipeline.
- `githubRelease` is a native step (`internal/shiprig/forge/forge.go`),
  GitHub-only. `Mode = Auto | GitHub | None`. Four probes behind one `Runner`:
  `git remote get-url origin` (must contain `github.com`), `gh auth status`,
  `gh release view <tag>`, `gh release create`. Notes are lifted from the package's
  `CHANGELOG.md` `## {version}` section. The step is **optional and auto-skipping**:
  no `gh` / non-GitHub origin ⇒ "tags only", never an error.
- `gitutil.PackageTag`: Go ⇒ `dir/vX.Y.Z`; everything else ⇒ `name@version`.
- Ecosystem publish differs: Go and `regex` are **tag-native** (`Publish` returns
  `Skipped: "released via git tag"`); npm / cargo / dotnet push a registry first,
  then get tagged.
- **No issue-tracker concept anywhere** in the tree.

## Part 1 — A legible step model

### 1a. Promote `tag` to a first-class step

- New canonical order:
  `version → commit → publish → **tag** → push → release → artifacts`.
- `publish` **stops tagging**: in the pipeline it always runs with `--no-git-tag`
  (the flag already exists in `cli/publish.go`); its job narrows to the registry
  push (and a clean no-op for Go / `regex`).
- The `tag` step creates the local annotated tag via `gitutil.CreateTag` — exactly
  what the existing `shiprig tag` command already does; `push --follow-tags` puts
  it on the remote.
- **Invariants preserved.** A tag still happens *after* a successful `publish`
  (step ordering), and the tag is on the remote *before* `release` runs — which the
  forge step needs, else `gh release create` cuts a divergent tag at HEAD
  (`forge.go:113–116`).
- **Ecosystem honesty.** Go / `regex` `publish` becomes a clean no-op and the
  `tag` step does their real release work — clearer than today's "publish that's
  secretly just a tag."
- **Behavior change (desirable).** Nothing hits the remote until the explicit
  `push` step — a cleaner rehearsal/dry boundary that pairs with `--rehearse`.

### 1b. Rename `githubRelease` → `release`

- A neutral name that reads right for any forge (knope calls this step `Release`;
  release-it's `github`/`gitlab` plugins produce a "GitHub Release" / "GitLab
  Release" — our current `githubRelease` echoes that label).
- `githubRelease` is kept as a back-compat **alias** in step-name resolution +
  config, so existing `release.jsonc` files keep working.
- The `forge` package stays; its `Mode`/handler naming generalizes in Part 2.

## Part 2 — Multi-forge releases

### 2a. Provider seam

Replace the four hard-coded `gh`/`git` probes with one interface (the `Runner`
test seam in `forge_test.go` is unchanged):

```go
type Release struct{ Tag, Title, Notes string }

type Provider interface {
    Name() string                                       // "github" | "gitlab" | "gitea"
    Matches(remoteURL string) bool                      // host check, for auto mode
    Ready(repoRoot string, run Runner) bool             // CLI present & authed
    ReleaseExists(tag, repoRoot string, run Runner) bool
    CreateRelease(r Release, repoRoot string, run Runner) error
}
```

The existing GitHub logic becomes `githubProvider`.

### 2b. Providers wrap the official CLIs

"Orchestrate, don't reimplement" — same stance as the `gh` step today:

| forge  | create                                    | exists            | auth / detect             |
|--------|-------------------------------------------|-------------------|---------------------------|
| github | `gh release create <tag> --title --notes` | `gh release view` | `gh auth status` / github.com |
| gitlab | `glab release create <tag> --name --notes`| `glab release view`| `glab auth status` / gitlab.com |
| gitea  | `tea release create --tag --title --note` | `tea release list`| `tea login list` / self-hosted host |

### 2c. Selection

- `forge: "auto"` iterates providers and picks the first whose
  `Matches(origin) && Ready()`.
- Only SaaS hosts (`github.com` / `gitlab.com`) are auto-detectable;
  **self-hosted GitLab/Gitea need an explicit `forge:` + `forgeURL`** (arbitrary
  hostnames can't be sniffed).
- `none` preserved (tags-only). The `Mode` enum collapses into provider selection;
  a missing CLI in `auto` just means "not ready" ⇒ skip (same degrade-to-tags-only
  contract as `gh` today, now per-forge).

### 2d. Config

`StepConfig.Forge` already exists — extend its values and add `ForgeURL`:

```jsonc
"release": { "forge": "gitea", "forgeURL": "https://git.example.com" }
```

`forge`: `auto | github | gitlab | gitea | none`.

## Part 3 — Issue-tracker integration

> Design agreed 2026-06-15. Parity target: on release, comment on / close issues
> referenced by the released changes, across the forge (GitHub/Gitea) and Jira.
> **Forge issues ship first; Jira is a deferred follow-up** (REST + token, no CLI
> — the most code). Comments are **idempotent via a hidden marker** so a re-run
> never double-comments.

### 3a. Ref-collection pass — `core/issuerefs` (pure)

A standalone parser over the **released commit range** (reuse
`gitutil.LatestModuleVersion` + `gitutil.LogSince` — the same range the changelog
uses, read from git so it works in both changeset and commit modes). It scans
each commit's subject+body; it does **not** touch `core/planner`.

```go
package issuerefs

type Kind int
const ( Forge Kind = iota; Jira )

type Ref struct {
    ID      string // "123" (forge) or "ENG-45" (Jira)
    Kind    Kind
    Closing bool   // preceded by a closing keyword (close/fix/resolve…)
}

// Collect parses forge refs (#123) and Jira refs (KEY-123, for the configured
// project keys) from commit messages, deduped by (Kind, ID); Closing is the OR
// across occurrences.
func Collect(messages []string, jiraProjects []string) []Ref
```

- **Closing keywords** (GitHub/Gitea): `close|closes|closed|fix|fixes|fixed|`
  `resolve|resolves|resolved` before a `#N` ⇒ `Closing:true` (eligible to close);
  a bare `#N` is a mention (comment-only).
- Same-repo refs only in v1 (no `owner/repo#N` cross-repo).

This slice is pure and fully unit-testable with no side effects.

### 3b. `IssueProvider` seam + native `issues` step (forge issues)

Mirrors the forge `Provider`. `#N` refs route to the **release's forge** (reusing
the release step's `Selection`); each provider wraps the forge's issue CLI.

```go
type IssueRef struct{ ID, URL string }

type IssueProvider interface {
    Name() string
    Ready(repoRoot string, run Runner) bool
    Comment(ref IssueRef, body, repoRoot string, run Runner) error
    Close(ref IssueRef, repoRoot string, run Runner) error
}
```

| forge  | comment                         | close                  | mention/exists           |
|--------|---------------------------------|------------------------|--------------------------|
| github | `gh issue comment N --body`     | `gh issue close N`     | `gh issue view N --json comments` |
| gitea  | `tea issue …`                   | `tea issue close N`    | `tea issue …`            |

- **Idempotent comments:** the body carries a hidden marker
  `<!-- shiprig:released:<tag> -->`; before posting, the provider reads the
  issue's existing comments and skips if the marker is already present. `Close`
  is naturally idempotent (closing a closed issue is a no-op).
- **New native `issues` step**, opt-in (only when `issues.enabled`), runs **after
  `release`**: canonical order `… → release → issues`. Registered like the
  `build`/`release` native handlers (`nativeBuiltins` + `NativeStepDescription` +
  the handler map in `cli/release.go`). It collects refs (3a) over the released
  range, then comments/closes per config.
- Degrade-to-skip if the forge CLI isn't ready (same contract as the release
  step), never an error.

### 3c. Config (`core/config.Config`)

New top-level `issues` block (added to `sharedKeys` + a typed `Issues` field):

```jsonc
"issues": {
  "enabled": true,
  "comment": "Released in {{version}}",   // empty ⇒ no comment, close only
  "close": true,                          // close issues with closing keywords
  "jira": { "url": "…", "project": ["ENG"], "tokenEnv": "JIRA_TOKEN" }  // 3d
}
```

### 3d. Jira provider — deferred follow-up

Jira is the outlier: REST + token auth (`tokenEnv`), `KEY-N` refs, and "close" is
a workflow *transition*, not a simple close. It routes independently of the forge
and is the most code, so it ships as its own PR after 3a+3b land.

## Build slices (independent, in order)

1. **Promote the `tag` step (1a)** — ✅ DONE. `DefaultOrder` in
   `pipeline/resolve.go`; tagging moved out of `cli/publish.go` (always
   `--no-git-tag` in-pipeline); the `tag` built-in wires the `shiprig tag` logic.
2. **Rename `githubRelease` → `release` (1b)** — ✅ DONE. No alias kept (prerelease,
   no back-compat).
3. **Multi-forge `Provider` refactor (2a–2d)** — ✅ DONE. `forge.go` →
   `Provider` interface (`provider.go`) + github/gitlab/gitea wrapping
   `gh`/`glab`/`tea`; `Selection` replaces the `Mode` enum; `forge`/`forgeURL`
   config; `auto` picks the first provider whose host matches origin and whose
   CLI is ready. Per-forge degrade-to-tags-only. Tests cover selection, auto-
   detection, per-forge argv, and the Gitea asset-skip.
4. **Issue tracker (Part 3)** — design agreed; forge-first, Jira deferred:
   - **3a** `core/issuerefs` ref-collection pass (pure) — ⬜ next.
   - **3b** `IssueProvider` seam + github/gitea + native `issues` step + config
     (idempotent marker comments) — ⬜.
   - **3d** Jira provider (REST/token) — ⬜ deferred follow-up.

Each slice ships as its own PR off a worktree, with tests, leaving
`go test ./...` green.

## Open questions / risks

- **Self-hosted detection** — ✅ resolved: explicit `forge:` + `forgeURL`. Only
  `github.com`/`gitlab.com` auto-match; Gitea is explicit-only (`Matches` returns
  false).
- **`glab` / `tea` as hard deps** — ✅ resolved: per-forge `Ready()` probe; a
  missing/unauthenticated CLI degrades to tags-only (in `auto` it's "not ready" ⇒
  skip; explicit forge reports a named skip).
- **Gitea idempotency** — ✅ resolved: no `release view <tag>`, so `ReleaseExists`
  runs `tea release list` and matches the tag as a field. Asset upload to an
  *existing* release is unsupported by `tea` (assets attach only at create), so
  it's reported as a skip rather than failing the run.
- **Splitting `tag` out** changes partial-run semantics (nothing on the remote
  until `push`). I think that's *more* correct and pairs with `--rehearse` —
  confirm.
- **Jira auth/token model** — defer to Part 3's own doc.
- **Order vs `artifacts`** — `release` before `artifacts` is unchanged from
  RELEASE-PIPELINE-DESIGN.md; the only deltas there are the rename and the new
  `tag` step earlier in the chain.
