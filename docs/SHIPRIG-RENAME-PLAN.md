# Rename plan: `relrig` → `shiprig` (+ family casing convention)

> Status: **PLAN ONLY** — nothing renamed yet. This documents the agreed
> decision and the full blast radius so the change can be executed (and
> reviewed) in one deliberate pass.

## Decision (settled)

After working the naming tradeoffs end to end:

- **`shipRig`** is the release tool — renamed from `relrig`. "rel" was a cryptic
  abbreviation inconsistent with the full-word siblings; `shipRig` is a clean
  verb, scans correctly in **every** context (even lowercase `shiprig`, with no
  garden-path), and gives the family a verb cadence: **rig** (run it) →
  **changeRig** (change it) → **shipRig** (ship it).
- **`changeRig`** is **kept** (not renamed to `bumprig`/`deltarig`). Its only
  flaw was the "changer" garden-path, which the casing convention fixes
  (`change|Rig`). "change" is also the most self-explanatory root for a changeset
  tool and keeps the `changeset` alias coherent — so renaming it would *cost*
  clarity and a second blast radius for no gain.
- **Casing convention** (the actual fix for compound-name misreads): capital
  **`Rig`** as the shared brand mark in prose/headings, lowercase binaries and
  commands everywhere code is literal. This is the GitHub / TypeScript / ESLint
  pattern — type `gh`, write "GitHub".

Tagline: **"Change It, Ship It."**

## Casing convention (write this down so it stays consistent)

| Context | Form | Examples |
|---|---|---|
| Prose, headings, brand, marketing | **`Rig`** capitalized | `shipRig`, `changeRig`, `claudeRig` |
| Binaries, commands, code, code blocks, paths, package names, archive names, env vars | lowercase | `shiprig`, `$ shiprig publish`, `bin/shiprig`, `shiprig_1.2.3_darwin_arm64` |
| Bare root tool | lowercase `rig` | the launcher is a single morpheme; no capital needed |
| Go doc comments | lowercase (matches the command + current `relrig` comment style) | `// shiprig layers publish/tag on top of the shared engine` |

The only way this backfires is inconsistent application — pick the form by
context, not by feel.

## Scope decision: directory + Go module path

This is the one consequential fork. Today:

| Tool | Dir | Module path | Binary |
|---|---|---|---|
| changeset tool | `changerig/` | `github.com/rigsmith/changerig` | `changerig` |
| release tool | `release/` | `github.com/rigsmith/release` | **`relrig`** |

So the release tool's dir/module already use the *domain* word ("release"),
while `changerig` uses dir = module-leaf = binary. The binary is the only place
"relrig" actually appears as the identifier.

**Confirmed: the `release` module has no external Go importers** (the lone
`rigsmith/release` grep hit was a false positive — a `rigsmith/releases` GitHub
API URL in `cli/internal/cli/selfupdate_test.go`). So a dir/module rename is
fully contained to the `release/` module and is compiler-verified.

Two options:

- **Option A — Full rename (recommended).** `release/` → `shiprig/`, module
  `github.com/rigsmith/release` → `github.com/rigsmith/shiprig`. Makes the family
  uniform (dir = module-leaf = binary, matching `changerig`). Churn: the dir
  move, `go.work`, and ~8 internal `rigsmith/release/internal/...` import lines —
  all mechanical, all caught by `go build`/`go test`, zero external fallout.
- **Option B — Minimal.** Keep `release/` and `github.com/rigsmith/release`;
  rebrand only the binary/command. Lowest churn, and "release" is a defensible
  domain name for the package — but leaves a dir/module that doesn't match the
  binary, perpetuating the existing asymmetry the rest of this effort is removing.

**Recommendation: Option A** — the whole exercise has been about family
consistency, the rename is contained and safe, and `changerig` already sets the
dir = binary precedent. The rest of this plan assumes A; deltas for B are noted
inline.

## Touch points

### 1. Build & distribution (user-facing binary name)

- **`.goreleaser.yaml`** — id `relrig` → `shiprig`; `binary: relrig` → `shiprig`;
  archive `id`/name list/`name_template` (`relrig_{{...}}` → `shiprig_{{...}}`);
  the commented brew cask block (`name: relrig`, `bin.install "relrig"`); the
  commented `changerig` block can stay as-is or be updated to `changeRig` casing
  in its comment. Header comment lines 5–13 referencing `relrig`.
  - **Option A also:** `main: .` stays (still the module root), but if the dir
    moves the goreleaser `dir:`/path for this build id must point at `shiprig/`.
- **`scripts/install.sh`** —
  - target parsing: accept `shiprig`; **keep `relrig` as a deprecated alias** that
    maps to the same download (lines ~132, 142, 146).
  - usage/help text and the example URLs (lines 13, 15, 18, 74) — `relrig.sh` →
    `shiprig.sh` (keep `relrig.sh` working as a redirect; see Domains).
- **`scripts/dev-install/main.go`** — the `relrig-dev` launcher derivation (1
  ref). Verb is discovered from `go.work`, so under Option A it follows the dir
  rename automatically; verify the generated launcher name becomes `shiprig-dev`.

### 2. The release module itself (Option A: also dir + module path)

- `release/` → `shiprig/` (git mv).
- `release/go.mod`: `module github.com/rigsmith/release` → `.../shiprig`.
- `go.work`: `./release` → `./shiprig`.
- Internal imports `github.com/rigsmith/release/internal/...` → `.../shiprig/...`
  in: `release/main.go`, `internal/cli/{dashboard,planeditor,planeditor_test,release,reporter,reporter_test}.go`.
- **`release/main.go`** — package doc comment (1 ref) → lowercase `shiprig`.
- **`release/internal/cli/root.go`** — `Use: "relrig"` → `"shiprig"`; `Short`/`Long`
  help text; the "Bare `relrig` behaves like `relrig add`" comment; package doc
  comment (lines 1, 22–24, 30).
- **`release/internal/cli/release.go`**, **`reporter_test.go`**, **`dashboard_test.go`**
  — command-name strings / expected output in tests (2 + 8 + 10 refs).

### 3. Shared UI string (runtime, user-facing)

- **`changerig/commands/ui.go:116`** — renders the literal `"relrig"` as a menu
  title. Because `changerig` exports the `commands` package `relrig` reuses, this
  string shows in the **shipRig** UI. Don't hard-swap to `"shiprig"` — it's shared
  by both tools. **Parameterize it** (pass the program name / use `root.Use`) so
  each tool shows its own name, or branch on the invoked binary. Flagged as the
  one spot needing a real (small) code change, not a find-replace.

### 4. Doc comments mentioning the tool (prose in code → lowercase `shiprig`)

Each is a single conceptual mention of "relrig" the tool:
- `cli/internal/detect/detect.go:4`
- `cli/main.go:4`
- `core/plugin/protocol.go:92`
- `core/ecosystem/gomod/gomod.go:170`
- `clauderig/main.go:5`
- `changerig/main.go:3`
- `changerig/commands/workspace.go:3`

### 5. Markdown docs (apply casing: `shipRig` in prose, `shiprig` in code blocks)

- `README.md` (9), `release/README.md` (6 — also rename file path if dir moves),
  `cli/README.md` (1), `core/README.md` (1), `examples/demo/README.md` (4).
- `docs/`: `RELEASE-ORCHESTRATOR.md` (17), `UI.md` (21), `FEATURE-PARITY.md` (9),
  `ARCHITECTURE.md` (5), `PORTING-PLAN.md` (4), `DISTRIBUTION.md` (3),
  `WEBSITE.md` (6), `CLAUDERIG-DESIGN.md` (1).
- **Also adopt `changeRig`/`claudeRig` casing** in these same docs as part of the
  convention rollout (separate find, same pass). Optional but recommended for
  consistency; binaries/dirs for those two are unchanged.

### 6. Historical / session records — **leave as-is**

`parity-session-notes.md` (5), `claude-questions.md` (5), `test-parity.md` (10)
are point-in-time logs. Rewriting them rewrites history. Recommendation: **don't
touch**, optionally add a single dated line ("`relrig` was renamed to `shiprig`
on 2026-06-12") at the top of `test-parity.md` if it's treated as living.

## Back-compat: keep `relrig` working

- **CLI alias:** ship a `relrig` alias for the `shiprig` binary (mirror whatever
  mechanism aliases `changeset` → `changerig`). Simplest: a `relrig` symlink/
  wrapper installed alongside, or argv[0] detection in `root.go`. Print a
  one-line deprecation notice when invoked as `relrig`.
- **install.sh:** keep accepting `relrig` as a target (maps to the shiprig
  download).
- **Domains:** `relrig.sh` / `relrig.dev` stay registered as **redirects** to the
  shiprig equivalents (consistent with the docs-site plan — `*.dev` redirect,
  `*.sh` = `curl | sh`). No code change here now; just don't retire them.

## Verification

1. `go build ./...` from the workspace root (catches every missed import — esp.
   under Option A).
2. `go test ./...` (the `release`/`shiprig` CLI tests pin command-name strings).
3. `git grep -wi relrig` → should return **only**: the back-compat alias code,
   `install.sh`'s alias branch, the historical notes, and domain-redirect
   mentions. Anything else is a straggler.
4. `goreleaser check` (validates the config parses after the id/binary edits).
5. Smoke: `shiprig --help` shows the new name; `relrig --help` still works and
   prints the deprecation notice.

## Suggested commit slicing

1. `shiprig: rename binary + command (relrig → shiprig)` — Option A dir/module
   move, `go.work`, goreleaser, root/main, tests, dev-install. Compiles & passes.
2. `shiprig: keep relrig as a deprecated alias` — alias wiring + install.sh +
   deprecation notice.
3. `ui: show the invoking tool's name (was hardcoded "relrig")` — the shared
   `ui.go` parameterization.
4. `docs: adopt Rig-casing (shipRig/changeRig/claudeRig) + relrig→shipRig` —
   all markdown + code doc-comments.

Keep each commit green so the rename is bisectable and the alias/UI changes are
reviewable on their own.
</content>
