# Release orchestrator (`relrig release`) — design

> Status: **design, not built — ON HOLD** (2026-06-11). This maps the net-changesets
> `changeset release` orchestrator (fully implemented there on the `jcamp` branch,
> `src/Changesets/Commands/Release/`, `docs/release-command-design.md`) onto the
> rigsmith Go architecture, so it's ready to build when greenlit. Nothing here is
> implemented in Go yet.

## What it is

One command — `relrig release` — that runs the whole release chain end to end:

```
version → commit → publish → push → githubRelease
```

…where **every step is toggleable, reorderable, hook-wrapped, and overridable
with your own scripts**, and step inputs (e.g. an npm OTP) can be captured from
arbitrary commands at run time. It is a **thin sequencer over steps relrig
already has** (`version`, `publish`, `tag`) plus one genuinely new capability
(forge/GitHub releases). It is NOT a second versioning/changelog tool — the
engine in `core` stays the engine; `release` only orchestrates it.

The design discipline (from the source doc): keep the orchestration layer thin
glue. The trap is letting `release.jsonc` drift into a general-purpose task
runner — that's knope's lane and the maintenance treadmill to avoid.

## The config: `.changeset/release.jsonc`

JSONC, **entirely optional** — with no file, `relrig release` runs the built-in
pipeline with defaults. Proposed Go shape (mirrors net-changesets `ReleaseConfig`):

```jsonc
// .changeset/release.jsonc
{
  // Named captures injected via ${vars.name}. `lazy` defers the capture until
  // first referenced, so a time-limited secret (OTP) stays fresh.
  "vars": {
    "npmOtp": { "command": ["op", "item", "get", "npm registry", "--otp"], "lazy": true }
  },

  // Per-step config, keyed by step name (built-in or custom).
  "steps": {
    "commit":  { "before": ["rig test"], "message": "chore: release" },
    "publish": {
      "args": ["--otp", "${vars.npmOtp}"],
      "after": ["./scripts/notify-slack.sh ${package.name}@${package.version}"],
      "confirm": "Publish to the registry?"
    },
    "githubRelease": { "forge": "auto" }
  },

  // Global hooks bracketing the whole run.
  "hooks": { "before": ["rig build"], "onError": ["./scripts/rollback.sh"] },

  // Reorder / splice custom steps anywhere.
  "order": ["version", "commit", "publish", "smoke", "push", "githubRelease"]
}
```

### Top-level keys

| Key | Go type (proposed) | Meaning |
|---|---|---|
| `order` | `[]string` | Ordered step names; overrides the default. Built-ins or custom steps. |
| `steps` | `map[string]StepConfig` | Per-step configuration. |
| `hooks` | `Hooks{ Before, After, OnError []Command }` | Run once around the whole pipeline. |
| `vars` | `map[string]Var{ Command, Lazy }` | Capture trimmed stdout; inject via `${vars.*}`. |

> **`tool` is omitted on purpose.** net-changesets has a `tool` key (default
> `changeset`) because its built-ins shell out to the `changeset` CLI (or
> `npx changeset`). In Go, **relrig is the tool** — the `version`/`publish` steps
> call relrig's own engine/commands directly, so there's no delegation base
> command to configure.

### Per-step (`StepConfig`)

| Field | Meaning |
|---|---|
| `enabled` | `*bool` — null = default (built-ins on; a custom step is on when it has a command). |
| `before` / `after` | `[]Command` — run around the step's own action. |
| `run` | `[]Command` — override a built-in's action, or define a custom step's action. |
| `args` | `[]string` — appended to a built-in command (e.g. `["--otp", "${vars.npmOtp}"]`). |
| `message` | commit-message template (the `commit` step). |
| `confirm` | `true` / string / `false` — pause-and-ask gate before the action; bypassed by `--yes`. |
| `forge` | `auto` / `github` / `none` — for `githubRelease`. |

### `Command` (shell vs argv)

A command is **either** a shell string (pipes/`&&`/redirection work) **or** an
argv list (exec'd directly — the safe form for injected secrets, no quoting
hazards). Mirror `CommandSpec` exactly:

```jsonc
"before": ["rig test"],                         // shell string
"run":    [["gh", "release", "create", "..."]]  // argv list
```

## Default pipeline → relrig mapping

| Step | net-changesets built-in | rigsmith Go mapping |
|---|---|---|
| `version` | `changeset version` | call `core/planner` + adapters in-process (relrig's `version`); auto-skip when no changesets |
| `commit` | `git add -A && git commit` | `gitutil` (new `Commit`); `message` template |
| `publish` | `changeset publish` | relrig's `publish` (the per-ecosystem adapters already built); **OTP/`args` injection point** |
| `tag` | per-package git tags | already built (`gitutil` + `tagName`); usually folded into publish, available standalone |
| `push` | `git push --follow-tags` | `gitutil` (new `Push`) |
| `githubRelease` | `gh release create <pkg>@<ver>` per package | **the only genuinely new code** — native step via `gh`, notes lifted from each `CHANGELOG.md`, idempotent (skip existing), graceful when `gh`/GitHub absent |

Default order is `version → commit → publish → push → githubRelease` (note:
`tag` is an available built-in but not in the default order — tags come from
publish, and push carries them with `--follow-tags`). Steps are **idempotent and
the run is resumable**: a mid-pipeline failure resumes with `--only githubRelease`.

## Interpolation, vars, secrets

`${...}` substitution in command text:

- `${env.NAME}` — environment variable.
- `${version}`, `${package.name}`, `${package.version}`, `${tag}`, `${changelog}`
  — the last two are per-package inside `githubRelease`.
- `${vars.*}` — a captured variable; **lazy** ones are run on first reference and
  cached (the npm-OTP-from-`op` case), so short-lived secrets stay fresh.

A secret masker redacts captured `vars` values in all output; `--dry-run` never
captures, so secrets stay literal in the printed plan.

## CLI surface (`relrig release`)

```
relrig release
  --dry-run        # print the resolved step plan + exact commands (secrets masked); run nothing
  --only  <steps>  # run only these steps
  --skip  <steps>  # skip these steps
  --from  <step>   # start from this step (resume)
  --to    <step>   # stop after this step
  --git-only       # suppress the forge release; tags still created
  --yes  / -y      # non-interactive (CI): no prompts, plain output
  --ui / --no-ui   # force/disable the rich TUI (default: auto by TTY)
  --config <path>  # alternate release config
```

`version`/`publish`/`tag` remain usable standalone — `release` just sequences them.

## Optional TUI

A full interactive bubbletea TUI (mirrors `TuiReleaseReporter` + the interactive
step picker): plan table, per-step rules, masked command/output lines,
success/cancel/failure panels with a `--only <step>` resume hint, on the same
event stream as the plain reporter. `--ui`/`--no-ui`, auto-by-terminal default.
Sequential (not single-frame-live) so mid-run `confirm` gates prompt normally.
This builds on the `ui` menu already in changerig/relrig.

## Go architecture notes (decisions to make at build time)

1. **`packages.versionRegex` ≈ an ecosystem adapter.** The source design has an
   optional `packages` registry to version arbitrary files via a named-capture
   regex (`{ "file": "Chart.yaml", "pattern": "version: (?<version>.*)" }`). That
   is functionally our `plugin.Ecosystem.SetVersion` for a one-off manifest. **Unify
   it**: a `regex` built-in ecosystem adapter (or a config-declared adapter)
   rather than a second version-stamping mechanism. This keeps "version read/write"
   in one place (the adapter contract) per [PLUGIN-PROTOCOL.md](PLUGIN-PROTOCOL.md).

2. **Cascade fallback.** net's design says: native graph where the ecosystem
   exposes one (npm/pnpm/Cargo/.NET), **declared-edge fallback** otherwise (for
   regex/other packages). Our `core/planner` already cascades over
   `plugin.Package.Dependencies`; the only gap is letting `release.jsonc`/the
   registry **declare** edges for adapters that can't resolve a graph.

3. **`confirm` is the home for the publish-safety prompt.** The open question
   logged in [claude-questions.md](../claude-questions.md) (should `publish` prompt
   before a real push?) is answered by per-step `confirm` here — keep `relrig
   publish` non-interactive, and let `release`'s `confirm` gate add the prompt.

4. **Steps reuse, don't reimplement.** `version`/`publish`/`tag` are already
   in-process in relrig/core; the pipeline calls them as functions (not by
   shelling `relrig version`), except where a step is a user `run` command.

5. **Custom steps + hooks + vars are ecosystem-neutral** — pure command
   execution + `${...}` interpolation + secret masking. No new engine work; this
   is the bulk of the orchestrator and it's all glue.

## Suggested phasing (when greenlit)

1. Headless pipeline engine: `release.jsonc` parse → resolved step plan →
   built-in `version`/`commit`/`tag`/`push` (in-process) + hooks + custom `run`
   steps + `${...}` interpolation + `--dry-run`/`--only`/`--skip`/`--from`/`--to`/
   `--config`, plain reporter.
2. `publish` step wired to the existing adapters + lazy `vars` capture + secret
   masking (delivers the npm-OTP-from-`op` case).
3. `githubRelease` via `gh`: `forge` auto/github/none (+ `--git-only`), per-package,
   notes from CHANGELOG, idempotent, graceful when `gh`/GitHub absent.
4. Rich bubbletea TUI + interactive step picker; `--ui`/`--no-ui`.
