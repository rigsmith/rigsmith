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

## Part 3 — Issue-tracker integration (roadmap; bigger)

Parity target: on release, comment on / close issues referenced by the released
changes, across GitHub / Gitea / Jira. This is a **new subsystem**, not a forge
tweak — the data it needs (referenced issue refs) doesn't exist yet, and it
touches `core/planner`, not just the forge step. Own design pass before build.

### 3a. Ref-collection pass

Over the released commit range (the same range the changelog uses, in
`core/planner`), collect issue refs — `#123` (GitHub/Gitea) and `KEY-123` (Jira) —
from commit footers (`Closes #12`) and/or changeset metadata.

### 3b. New native `issues` step + provider seam

```go
type IssueRef struct{ ID, URL string }

type IssueProvider interface {
    Comment(ref IssueRef, body string, run Runner) error
    Close(ref IssueRef, run Runner) error   // Jira = transition
}
```

backed by `gh issue`, `tea issue`, and Jira REST.

### 3c. Config (`core/config.Config`)

```jsonc
"issues": { "provider": "jira", "url": "...", "project": "ENG",
            "comment": "Released in {{version}}", "close": true }
```

### 3d. Caveat

Jira is the outlier — REST + token auth, no convenient CLI — so it's the most
code. Defer to this part's own doc expansion.

## Build slices (independent, in order)

1. **Promote the `tag` step (1a)** — `DefaultOrder` in `pipeline/resolve.go`; move
   the tagging phase out of `cli/publish.go` (always `--no-git-tag` in-pipeline);
   wire the existing `shiprig tag` logic as the built-in. Tests for Go (tag-native)
   and node (registry-then-tag) ordering.
2. **Rename `githubRelease` → `release` + alias (1b)** — `resolve.go` name
   handling, config docs, README; the alias keeps old `release.jsonc` working.
3. **Multi-forge `Provider` refactor (2a–2d)** — `forge.go` → provider interface +
   github/gitlab/gitea; `Forge`/`ForgeURL` config; auto-detection.
4. **Issue tracker (Part 3)** — its own design pass first, then planner
   ref-collection + `issues` step + providers.

Each slice ships as its own PR off a worktree, with tests, leaving
`go test ./...` green.

## Open questions / risks

- **Self-hosted detection** — require explicit `forge:` + `forgeURL`, or attempt a
  heuristic (Gitea API ping / `.gitlab-ci.yml`)? Start explicit.
- **`glab` / `tea` as hard deps** — match the `gh` "degrade to tags-only if the CLI
  is missing" contract per-forge; in `auto` a missing CLI is just "not ready".
- **Gitea idempotency** — no `release view <tag>`; use `tea release list` + match.
  Confirm the shape.
- **Splitting `tag` out** changes partial-run semantics (nothing on the remote
  until `push`). I think that's *more* correct and pairs with `--rehearse` —
  confirm.
- **Jira auth/token model** — defer to Part 3's own doc.
- **Order vs `artifacts`** — `release` before `artifacts` is unchanged from
  RELEASE-PIPELINE-DESIGN.md; the only deltas there are the rename and the new
  `tag` step earlier in the chain.
