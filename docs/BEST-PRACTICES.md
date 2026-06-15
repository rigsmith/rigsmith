# Best Practices

A field guide distilled from how **rigsmith** is built, maintained, and used.
It is organized by audience:

1. [**Engineering patterns**](#1-engineering-patterns) — the design conventions
   this codebase embodies, written so they're reusable in any Go project.
2. [**Contributing to rigsmith**](#2-contributing-to-rigsmith) — how to work
   *in* this repo: branches, worktrees, hooks, commits, releases.
3. [**Using the tools**](#3-using-the-tools) — how to get the most out of
   `rig`, `changerig`, `shiprig`, and `clauderig` in your own repos.

For the *why* behind a given rule, follow the links into the design docs
([`ARCHITECTURE.md`](ARCHITECTURE.md), [`WORKTREE-DISCIPLINE.md`](WORKTREE-DISCIPLINE.md),
[`PLUGIN-PROTOCOL.md`](PLUGIN-PROTOCOL.md), [`RELEASE-PIPELINE-DESIGN.md`](RELEASE-PIPELINE-DESIGN.md)).

---

## 1. Engineering patterns

These are the conventions the codebase demonstrates. Each is a principle you can
lift into another project, with a pointer to where it lives here.

### Architecture & module layout

- **One module, many binaries, one shared engine.** A single Go module
  (`github.com/rigsmith/rigsmith`) holds every binary under `cmd/<tool>/` and the
  reusable engine under `core/`. Tool-specific machinery lives in
  `internal/<tool>/` so it can never leak into the public engine. One module
  means one `go.mod`, one version, atomic cross-cutting refactors.
- **Keep the dependency direction one-way.** `core/` knows nothing about the
  binaries; only `cmd/` and `internal/` import `core/`. The engine never imports
  `cmd` or `internal`. Enforce the boundary by putting anything binary-specific
  in `internal/`.
- **`main.go` is a thin shim.** Each `cmd/<tool>/main.go` just delegates to its
  `internal/<tool>` package (e.g. `cli.Execute()`). Keep argument wiring and
  business logic out of `main`.

### Zero-dependency core

- **The engine depends on nothing but the standard library.** Packages like
  `core/semver`, `core/changelog`, `core/config`, `core/plugin`, and
  `core/pathmap` import only stdlib. External dependencies are confined to the
  CLI/TUI layer (cobra, bubbletea, lipgloss). This keeps the engine portable,
  fast to test, and trivially embeddable.
- **Curate, don't accumulate, dependencies.** Every direct dependency in
  `go.mod` is a CLI or TUI library — there is no transitive runtime stack. When a
  needed dependency is dormant upstream, vendor a small fork rather than take a
  live dependency on an unmaintained package (see the vendored `core/fang`).
- **Ship a single static binary.** All binaries build with `CGO_ENABLED=0` and
  `-s -w` link flags (see [`.goreleaser.yaml`](../.goreleaser.yaml)). The
  north-star property: no runtime, installable via `curl | sh` / Homebrew /
  Scoop on any machine.

### Extensibility: the plugin contract

- **Make built-ins the reference implementation of the plugin contract, not a
  privileged bypass.** The in-process ecosystem adapters (`core/ecosystem/{node,
  dotnet,gomod,...}`) implement the *same* `plugin.Ecosystem` interface that the
  subprocess transport mirrors. Because the built-ins speak the contract, the
  contract stays honest. (`core/plugin/protocol.go`, [`PLUGIN-PROTOCOL.md`](PLUGIN-PROTOCOL.md))
- **Plugins are stateless one-shot JSON functions.** A plugin is invoked as
  `cmd <method>` with JSON on stdin and a result on stdout — no daemon, no gRPC,
  no shared state. Easy to write in any language, easy to test by piping JSON.
- **Version the contract and reject mismatches loudly.** `APIVersion` is pinned;
  a plugin that doesn't recognize the version must exit non-zero rather than
  guess. Capabilities are declared (`info` lists what the plugin can do) instead
  of discovered by trial.
- **Resolve plugins by convention.** A config value of `"default"` → built-in; a
  path (`./x` or contains `/`) → run directly; a bare name → look up
  `changeset-changelog-<name>` on `$PATH` (the git-subcommand convention).

### Testing

- **Pin behavior with a language-neutral golden corpus.** `core/testdata/parity/`
  freezes scenarios verified to be identical across Node `@changesets`, C#
  net-changesets, and Go rigsmith. Cross-implementation goldens catch drift no
  unit test would.
- **Write a test that names the bug it prevents.** Tests here carry comments like
  *"pins the fix where SetVersion rewrote EVERY 'version' key, including nested
  publishConfig.version."* A regression test is documentation; say what it
  guards.
- **Keep tests hermetic.** Use `t.TempDir()` and direct file I/O; assert the
  decision (e.g. *private package is skipped*) before any external toolchain is
  invoked, so the suite runs anywhere without npm/dotnet installed.
- **Share setup with small helpers, not frameworks.** Table-driven tests plus a
  handful of helpers (`writeFile`, `discoverNames`, `wantNames`) keep cases terse
  without adding a test dependency.

### Configuration & cross-OS

- **Accept JSONC for human-edited config.** `.rig.json`, `.changeset/config.json`,
  and `release.jsonc` tolerate comments and trailing commas, and edits preserve
  formatting (`core/jsonc`). Config humans touch should forgive humans.
- **Layer config with a clear precedence.** Settings merge global → repo → local,
  and an explicit flag always wins over a config default (e.g. `--quiet` beats
  `.rig.json`). Apply defaults explicitly when a key is missing.
- **One spelling for every toggle.** A single `Truthy()` helper accepts
  `1/true/yes/on` (case-insensitive) for *every* env toggle, so users never
  guess which spelling a given flag wants.
- **Make cross-OS paths a token problem, not a string problem.** `core/pathmap`
  expands `$HOME` / `${APPDATA}`-style templates for a target OS with a
  visited-set cycle check, so a self-referential token yields a clean error
  instead of a stack overflow.
- **Wrap errors with context.** `fmt.Errorf("dotnet pack: %w", err)` — preserve
  the chain with `%w` and prepend the operation, so failures read top-down.

### CLI design

- **Convention over configuration.** `rig build` / `test` / `run` work in any
  repo with zero config by auto-detecting the ecosystem and running the native
  command. Config is an override, never a prerequisite.
- **Interactive on a TTY, scriptable everywhere else.** A bare command on a
  terminal opens a context-aware menu; the same command with flags, or off a TTY,
  is non-interactive and CI-safe. Branch on `isatty`, never assume a human.
- **Make destructive actions idempotent and confirm-gated.** `shiprig publish`
  skips versions already on the registry; `tag` skips existing tags; confirm
  prompts gate mutations on a TTY and `--yes` bypasses them in CI.
- **Enable prefix matching for ergonomics.** `rig cove` → `coverage`. Cheap to
  turn on (`cobra.EnablePrefixMatching = true`), and it makes the CLI feel fast —
  but only built-ins prefix-match; discovered verbs require an exact name so they
  never shadow a built-in.
- **Let one source feed both dev and release.** The same ecosystem adapter that
  drives releases also exposes `DevCommands`, so `rig` and `shiprig` learn an
  ecosystem's commands from a single definition.

---

## 2. Contributing to rigsmith

The repo is **convention-first and self-enforcing**: a guard hook, git hooks, and
CI catch mistakes early. Work *with* them.

### Worktree & branch discipline

Enforced by the `clauderig guard` PreToolUse hook. Full spec:
[`WORKTREE-DISCIPLINE.md`](WORKTREE-DISCIPLINE.md). The short version:

- **Never write code on `main`/`master`.** Make a branch + worktree first:
  `rig worktree new <branch>`. It creates a sibling checkout at
  `<repo>-worktrees/<branch>` and opens it in a *new* editor window for review.
- **Docs and root config may go on the base branch directly** — `*.md`, `docs/`,
  `.github/`, and top-level config (`*.toml`, `*.yml`, `*.json`, `LICENSE`,
  `.gitignore`). Everything else is code and needs a PR.
- **Don't move the session's working directory.** The editor keys chat history to
  the folder path, so never `cd` out of the repo root or use Enter/Exit-worktree
  tools. Act elsewhere with absolute paths, `git -C <dir> …`, or a subshell
  `(cd <dir> && …)`.
- **One window pinned to the primary repo** as the continuous chat; treat
  worktree windows as review/diff only.
- **Override only when you must** change base-branch code:
  `export CLAUDERIG_ALLOW_MAIN=1` (session) or `touch .claude/allow-main` (repo).

### Local git hooks (lefthook)

- **Install hooks once per clone:** `lefthook install`.
- **Pre-commit runs `gofmt`** — unformatted Go fails the commit (`gofmt -w .`).
- **Pre-push refuses to clobber `main`** — if local `main` has diverged from
  `origin/main`, the push is blocked so a force-push can't erase merged PRs.
  Bypass deliberately with `git push --no-verify`.

### Commits & PRs

- **Conventional Commits with a scope:** `type(scope): description` —
  `feat(cli): add rig deps`, `fix(prune): render plan inside the confirm dialog`.
  `feat` and `fix` drive the generated changelog; `docs`/`test`/`chore` are
  excluded. Observed scopes: `cli`, `ui`, `devtools`, `release`, `prune`,
  `status`, `worktree`, `config`.
- **Squash-merge with the PR number appended** — history reads
  `feat(cli): add rig deps (#66)`. Keep one logical change per PR.
- **Always verify a merge actually landed in `origin/main`.** This is a private
  repo without branch protection; the pre-push hook is the backstop, not a
  guarantee.

### Tests & CI

- **Run `go test ./...` before pushing.** CI runs it on Linux, macOS, and Windows
  (fail-fast disabled), plus `go vet`, `gofmt`, and a gitleaks secret scan.
- **Normalize line endings to LF** (`.gitattributes`) — parity goldens and
  Windows checkouts depend on it.
- **When you change release-engine behavior, regenerate and review the parity
  goldens** (`scripts/regen-parity-goldens.mjs`) so the cross-implementation
  corpus stays the source of truth.
- **Secret scanning allowlists synthetic fixtures only.** If a *real* secret-like
  string is intentional test data, give it an obviously-fake/sequential value and
  allowlist it narrowly in `.gitleaks.toml` — never disable the scanner.

### Releasing

- **Changesets drive versions.** Add a changeset on PRs that change behavior
  (`changerig add`); the `require-changeset` Action blocks merge if one is missing
  (label `skip-changeset` to opt out). See [`GITHUB-ACTIONS.md`](GITHUB-ACTIONS.md).
- **Rehearse before going live.** `shiprig release --dry-run` previews the plan;
  `shiprig release --rehearse` does a real local build to `dist/` that commits and
  publishes nothing. See [`RELEASE-PIPELINE-DESIGN.md`](RELEASE-PIPELINE-DESIGN.md).
- **The install script is the single source of truth** (`scripts/install.sh`),
  copied to the published site at build time — never fork it into the site tree.

---

## 3. Using the tools

All four tools are **convention-driven** (zero-config works), **interactive by
default** (a TTY gets a menu; flags suppress prompts), and **idempotent** (safe to
re-run). When unsure what a tool can do, run it bare or with `ui`.

### `rig` — convention-first dev launcher

- **Start with `rig info`** to see the detected ecosystem, packages, and the dev
  commands rig will run — before invoking a verb.
- **`rig <verb>` runs repo-wide; `rig <verb> <project>` scopes to one package.**
  Matching is tiered (exact → prefix → substring), so `rig test Api` usually
  finds the one you mean.
- **In Node repos, every `package.json` script is already a verb** — `rig dev`,
  `rig lint` — with no config, as long as it doesn't shadow a built-in.
- **Iterate with `rig watch <verb>`** (or the position-independent `--watch`) to
  re-run on file changes.
- **Preview destructive verbs with `--dry-run` / `-n`** before running them.
- **Gate coverage with `rig coverage --min 80`** — it exits non-zero below the
  threshold, so it drops straight into CI.
- **Customize via `.rig.json` only when defaults don't fit** — `defaultProject`,
  `ecosystem` (to disambiguate a polyglot repo), and custom `commands`. It's
  JSONC, so comment it.
- **Run `rig doctor`** to check SDK pins and missing tools before filing a bug.

### `changerig` — changeset lifecycle

- **`changerig init` once** to scaffold `.changeset/`. Add `--source commits` if
  you'd rather drive versions from Conventional Commits and skip changeset files
  entirely.
- **`changerig add` with no flags is interactive** (pick packages, bump, write a
  summary); pass flags for scripts:
  `changerig add -p my/pkg --bump minor -m "…"`.
- **Preview with `changerig status --verbose`** before you commit — it shows the
  full pending release plan including dependency cascades.
- **Never hand-edit `.changeset/*.md`** — use `add` or the `ui` so the format
  stays valid.
- **Group packages that always move together** with `linked`/`fixed` in
  `.changeset/config.json`, and put test/example projects in `ignore` so they're
  never versioned.
- **`changerig version` bumps + writes `CHANGELOG.md`** (with the dependency
  cascade) but does not publish — use it when you want versioning without a
  release.

### `shiprig` — release orchestration

- **Bare `shiprig` shows `status`** — look before you leap.
- **Standard flow:** `shiprig version` → commit → push → `shiprig publish`. The
  step pipeline is configured in `.changeset/release.jsonc` (JSONC).
- **Rehearse first:** `--dry-run` is plan-only; `--rehearse` forces every
  mutating step into a safe variant (real build, no publish/commit/push).
- **Idempotent by design:** publish won't re-push a version already on the
  registry, and tags skip if they exist — safe to re-run after a partial failure.
- **CI:** confirm gates are TTY-only; pass `--yes` / `-y` to proceed
  non-interactively. Use `publish --no-push` to publish without pushing tags.
- **Custom changelog output:** set `"changelog": "<plugin>"` to swap in a
  subprocess generator (e.g. the changelogen reference plugin) — the built-in
  dogfoods the same JSON contract.

### `clauderig` — sync Claude Code config across machines

- **`clauderig init` once per machine** — it hard-gates the remote to a verified
  *private* GitHub repo, sets the machine identity, and picks which roots to sync.
- **Sync = snapshot → redact → commit → push.** Secrets are stripped before
  commit and a tripwire fails the sync loudly if one slips past — so re-auth
  (login, API keys, MCP) on a fresh machine is expected, not a bug.
- **Restore rewrites paths for the target OS** via `core/pathmap`
  (`/Users/...` ↔ `C:\Users\...`). Use `clauderig restore --backup` when
  `~/.claude` is non-empty so you have a rollback point.
- **`clauderig doctor --fix`** verifies the guard hook, the CLAUDE.md guide, and
  that `git`/`gh`/`code` are on `PATH`.
- **Install the worktree guard in a repo** with `clauderig project install` to get
  the same branch/PR discipline described in Part 2. See
  [`CLAUDERIG-DESIGN.md`](CLAUDERIG-DESIGN.md).

---

*This document is derived from the codebase and the design docs in `docs/`. When
a practice and the code disagree, the code wins — fix whichever is wrong.*
