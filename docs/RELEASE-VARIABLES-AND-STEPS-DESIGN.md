# Release pipeline: built-in variables, derived values & richer custom steps

Status: **proposed** (design only — nothing implemented yet)
Scope: `internal/shiprig/pipeline` + the host wiring in `internal/shiprig/cli/release.go`
Related: [RELEASE-PIPELINE-DESIGN.md](./RELEASE-PIPELINE-DESIGN.md), [RELEASE-STEPS-AND-FORGES-DESIGN.md](./RELEASE-STEPS-AND-FORGES-DESIGN.md), [RELEASE-NAMING-AND-MONOREPO.md](./RELEASE-NAMING-AND-MONOREPO.md), [FEATURE-PARITY.md](./FEATURE-PARITY.md)

## Why

shiprig already beats knope and release-it on pipeline customisation in most
respects (composable ordered steps **and** before/after/onError hooks **and**
captured `${vars.*}` **and** secret masking). Five gaps remain. This doc plans
all five as one coherent change, because they share an injection point and a
monorepo problem.

| # | Gap | knope | release-it | this doc |
|---|-----|-------|------------|----------|
| 1 | Built-in release variables (`${version}`, `${changelog}`, `${releaseUrl}`, `${tag}`) in custom commands | `Version`, `ChangelogEntry` — **single-package only** | `version`, `changelog`, `releaseUrl`, … — single-package tool | **monorepo-addressable** (§3) |
| 2 | Derived issue/branch values (knope's `IssueBranch`) | `IssueBranch` | — | `${issues}`, `${issueBranch}` (§5) |
| 3 | Override a native step (`build`/`release`/`issues`) with a custom command | any step is replaceable | hooks only | **already half-works** — document, test, gate (§6) |
| 4 | Human display name for a step, distinct from its `order` key | — | — | `name` field (§7) |
| 5 | Ecosystem targeting (run a step/hook only for Node / .NET / Go / Rust packages) | — | — | `ecosystems` field (§8) |
| 6 | Dry-run that previews *and* validates custom commands | logs only | logs only | preview + opt-in execution (§9) |

The headline win is #1 done *right*: **both competitors simply bail when
`$version` is referenced in a multi-package repo.** shiprig is monorepo-native,
so a bare `${version}` is genuinely ambiguous — the design below gives real
addressing instead of a dead end, while keeping a safe single-package shorthand.

## 1. The core architectural constraint

The pipeline engine is **package-blind**. It is a shell-command runner whose only
interpolation context today is `{"tool": …}` plus user `${vars.*}`:

- `run.go:153` — `p.baseContext = map[string]string{"tool": toolOf(config)}`
- `run.go:237-266` — `runCommands()` layers `${vars.*}` per command, then
  `interpolateCommand(context, p.env, command)` (`vars.go:123`).

Everything about the *release* — which packages, their new versions, changelog
notes, tags, forge release URLs, resolved issues — lives one layer up, in the
**host** (`cli/release.go`), and is produced by `ws.Discover(ctx)` **inside each
native handler at run time** (`release.go:114, 145, 163`). Each `plugin.Package`
carries `.Name`, `.Dir`, `.Version`; the ecosystem is `ecoOf[pkg.Name]`; the tag
is `gitutil.PackageTag(eco, dir, name, version)` (`release.go:175`); notes come
from `forge.extractNotes(pkg, repoRoot)` (`forge/forge.go:191`).

Two consequences drive the whole design:

1. **The engine cannot compute these values itself** — it has no workspace, no
   registry, no forge. The host must *supply* them.
2. **Timing.** `pkg.Version` only reflects the *new* version after the `version`
   step has stamped manifests; `${releaseUrl}` only exists after the `release`
   step has run. So the context must be **lazy and refreshable**, not a static
   map computed before `Run()`.

### Injection seam

Add a host-supplied provider, resolved on demand and cached per run, mirroring
how `${vars.*}` already work (`vars.go:35-89`):

```go
// pipeline/context.go (new)
//
// ReleaseContext supplies built-in interpolation values the engine cannot
// compute itself. Every method is called lazily, at most once per distinct key
// per run (results cached), so values that only exist mid-run (a stamped
// version, a forge URL) are read at the moment a command references them.
type ReleaseContext interface {
    // Packages returns the released packages in stable order. Used to expand
    // aggregate forms (${versions}) and to validate bare ${version}.
    Packages() []ReleasePackage
    // ReleaseURL returns the forge release URL for a package's tag, or "" if
    // not yet created / forge disabled.
    ReleaseURL(pkg string) string
    // Issues returns the resolved issue references for the release.
    Issues() []IssueRef
}

type ReleasePackage struct {
    Name      string // manifest name, e.g. "@scope/foo"
    Key       string // address key used in ${version.<key>} (see §4)
    Ecosystem string // "node" | "dotnet" | "go" | "rust"
    Version   string // new version after the version step
    Tag       string // gitutil.PackageTag(...)
    Changelog string // extractNotes(...) for this version
}

type IssueRef struct {
    Number int
    Branch string // the SwitchBranches-equivalent branch name
}
```

`pipeline.New(...)` (`run.go:122`) gains a `relCtx ReleaseContext` parameter
(nil ⇒ today's behaviour: only `${tool}`/`${vars.*}`/`${env.*}` resolve). The
host builds it in `cli/release.go` closing over `ws`, `ecoOf`, `built`,
`fsel`, and the forge run results — the same data the native handlers already
close over.

> The host wiring at `release.go:192` becomes
> `pipeline.New(runner, reporter, masker, prompter, ws.Root, releaseEnv,
> nativeSteps, relCtx)`.

## 2. Interpolation: where the new keys plug in

`interpolate`/`resolveKey` (`vars.go:139-193`) already walk `${…}` left-to-right
and resolve `env.*` specially. Extend `resolveKey` with the built-in namespaces
**before** the generic `context[key]` fallback, so they cannot be shadowed by a
stray context entry and so an *unknown* `${version.bad}` is reported rather than
left verbatim:

Resolution order for a `${…}` key:
1. `env.NAME` → existing env lookup (`vars.go:184`).
2. `vars.NAME` → existing per-command capture (`run.go:244-251`).
3. **`version` / `version.<key>` / `versions`** → from `ReleaseContext` (§3).
4. **`changelog` / `changelog.<key>`** → from `ReleaseContext`.
5. **`tag` / `tag.<key>` / `tags`** → from `ReleaseContext`.
6. **`releaseUrl` / `releaseUrl.<key>` / `releaseUrls`** → from `ReleaseContext`.
7. **`issues` / `issueBranch`** → from `ReleaseContext` (§5).
8. `tool` → existing.
9. otherwise `context[key]`, else left verbatim.

The engine asks `ReleaseContext` only when a referenced key needs it (lazy);
results are cached in the per-run context map. Resolved built-in values that are
package versions/URLs are **not** secret — do not auto-mask them (unlike
`${vars.*}`).

## 3. Monorepo-addressable built-in variables (the headline)

Each inherently-per-package value is exposed in **three forms**:

| Form | Example | Resolves to |
|------|---------|-------------|
| **Addressed** | `${version.foo}`, `${changelog.foo}`, `${releaseUrl.foo}`, `${tag.foo}` | that one package's value |
| **Aggregate** | `${versions}`, `${tags}`, `${releaseUrls}` | a formatted list over all released packages |
| **Bare** | `${version}`, `${changelog}`, `${releaseUrl}`, `${tag}` | the value **iff exactly one package is released**, else a hard error |

### Bare form: strictly better than the competitors

knope/release-it bail whenever `$version` is set and there's more than one
package. We keep that *safety floor* — a bare `${version}` in an ambiguous
release is a clear config error, never a silent wrong value — but we add the two
escape hatches they lack:

```
release: ${version} references a single version, but this release includes 3
packages (@scope/foo, @scope/bar, @scope/baz). Use ${version.<package>} for a
specific one, or ${versions} for all of them.
```

When the release is single-package, bare `${version}` *just works*, matching the
common case knope/release-it optimise for. (Determination is dynamic: it depends
on how many packages actually have changesets this run, via
`ReleaseContext.Packages()`.)

### Aggregate formatting

Default `${versions}` → `"@scope/foo@1.2.0, @scope/bar@0.3.1"` (each package's
full manifest **Name** + `@` + `Version`, sorted, comma-joined — the addressed
form `${version.<key>}` uses the short **Key** from §4, but the aggregate uses
the full name because that is what a human reads in a notification). This is the
shape a notification hook wants:

```jsonc
"hooks": {
  "after": "slack-notify 'Released ${versions}'"
}
```

If a richer layout is needed later, add a `varFormat` config block; out of scope
here — the comma list covers the 90% case.

### Addressing key (`<key>`)

`${version.<key>}` needs a stable, typeable key per package. Manifest names like
`@scope/foo` contain `/` and `@`, which are awkward inside `${…}`. Define `Key`
(see §4) as the **short package name** (`foo`), with collision handling, and
document that `${version.<key>}` uses it. `${version.@scope/foo}` MAY also be
accepted as an exact-name alias. This needs a decision — see Open questions.

### Timing guard

`${version.*}` referenced **before** the `version` step has run would read a
pre-bump (or empty) value. Options: (a) resolve from the *planned* bump (compute
versions up front from changesets, independent of stamping), or (b) error if
referenced before `version` ran. Prefer **(a)** so `before`-hooks on early steps
can use it — it matches how the plan is already previewable in `--dry-run`. The
host computes planned versions from the changeset set, not from re-reading
manifests.

## 4. `ReleasePackage.Key` (addressing) & collisions

- `Key` = last path segment of the manifest name (`@scope/foo` → `foo`,
  `github.com/org/mod/v2` Go module → `mod`), matching the spelling already used
  for tags/UI where possible (see `RELEASE-NAMING-AND-MONOREPO.md`).
- On collision (two packages share a short name across ecosystems), fall back to
  an ecosystem-qualified key (`node:foo`, `dotnet:foo`) **for the colliding
  packages only**, and emit one `log`-level note so the user knows the address.
- Always accept the **full manifest name** as an exact alias, so there is an
  unambiguous escape hatch regardless of collision logic.

Host builds the `Key` map once per run from `ws.Discover` output; the engine
treats `Key` as opaque.

## 5. Derived issue/branch values (knope `IssueBranch` parity)

shiprig already resolves issues from released commit messages
(`releasedCommitMessages` → `forge.RunIssues`, `release.go:178-185`). Surface
them:

- `${issues}` → aggregate, e.g. `"#42, #57"` (sorted, deduped).
- `${issueBranch}` → the branch name shiprig's issue automation would use,
  matching knope's `IssueBranch` semantics. Only meaningful when a single issue
  is in scope; in multi-issue releases it errors with the same
  "use the addressed form" pattern (`${issueBranch.42}`).

`ReleaseContext.Issues()` returns `[]IssueRef`; the engine formats. If issue
automation is disabled (`issues.enabled = false`, `release.go:159`), these
resolve to empty and a `before`-hook referencing them gets `""` (consistent with
`${env.*}` missing-var behaviour), **not** an error — referencing issues should
not force-enable the feature.

## 6. Native-step override (Feature 3 — already half-works)

**Finding:** `resolve.go:181-184` returns a non-nil `stepConfig.Run` *before* the
native check (`resolve.go:146`), so a `run` on `build`/`release`/`issues`
**already replaces** the native handler today and turns the step into a
`StepKindCommands` step. There is **no validation** rejecting it (confirmed:
nothing in the pipeline package or config loader guards this). It is simply
undocumented and untested.

So Feature 3 is mostly *specify, test, document* rather than new engine code:

1. **Decide semantics — replace vs augment.** Today `run` fully *replaces* the
   native action. Keep that as the contract (simple, predictable): "`run` on any
   step, built-in or native, replaces its action; use `before`/`after` to keep
   the native action and add around it." Document that a native step's action
   cannot be *augmented* in place — you either keep it (no `run`) or replace it.
2. **Tests.** Add cases to `pipeline_test.go`: `run` on `release` runs the
   command instead of the native handler; `before`/`after` on a native step
   still wrap the native action (already the case via `run.go:186-200`).
3. **Docs.** State it in the config reference and `init` starter comments.
4. **Optional guardrail.** If replacing `build`/`release` silently is judged too
   sharp a footgun, add a `log`-level note when a native step is overridden
   ("`release`: using custom run, native forge release skipped"). No hard error.

This composes with §7/§8: an overridden native step is just a command step and
gets `name`/`ecosystems` like any other.

## 7. Step display name (Feature 4)

`StepConfig` has no label; reporters render the raw `order` key (`step.Name`).
Custom steps like `smoke-test` read fine, but `db-migrate-prod` would prefer
"Migrate production DB" in the dashboard.

- Add `Name *string` (`json:"name"`) to `StepConfig` (`config.go:182`). Naming:
  the config key is `name` (the *display* name); the map key in `order` stays the
  step **id**. (Avoid calling the id "name" in docs to prevent confusion — in
  `ResolvedStep`, `Name` is the id; add `DisplayName string`.)
- `Resolve` (`resolve.go:163`) populates `ResolvedStep.DisplayName` =
  `stepConfig.Name` if set, else a humanised default (built-ins get curated
  labels; custom steps get the id). For native steps this can default to
  `NativeStepDescription(name)` (`resolve.go:34`).
- Reporters (`StepStarted`/`StepCompleted`/plan rendering — plain, rich,
  dashboard) render `DisplayName`, keeping the id only where an exact key is
  needed (e.g. `--only`/`--skip`/`--from`/`--to` still match the id).

## 8. Ecosystem targeting (Feature 5)

A custom `npm run build:assets` step is nonsense for a .NET-only release. Let a
step (and, by extension, its before/after hooks) declare which ecosystems it
applies to:

```jsonc
"steps": {
  "smoke-node": {
    "name": "Node smoke test",
    "ecosystems": ["node"],
    "run": "npm run smoke"
  }
}
```

- Add `Ecosystems []string` (`json:"ecosystems"`) to `StepConfig`. Empty/absent
  ⇒ all ecosystems (today's behaviour).
- **Semantics:** the step runs iff the release includes ≥1 package in a listed
  ecosystem. If it includes none, the step is **skipped with a reason**
  (`SkipReason = "no <ecosystems> packages in this release"`), surfaced in the
  plan exactly like `disabled`/`--skip` (`resolve.go:272-303`,
  `run.go:179-182`). This makes a polyglot config portable: the Node steps
  no-op cleanly in a Go-only release.
- **Validation:** unknown ecosystem strings are a config error at load
  (`LoadConfig`, `config.go:243`) — fail fast with the valid set.
- **Dependency:** `Resolve` currently takes only `config` + `opts`. To know which
  ecosystems are in the release it needs the package set. Pass the ecosystem set
  into `ResolveOptions` (host fills it from `ws.Discover`), keeping `Resolve`
  pure/testable. The engine never imports the workspace/registry.

This also pairs naturally with §3: a future `for-each-package` step mode could
reuse the same ecosystem filter, but that is **out of scope** here (this design
keeps steps whole-release; per-package fan-out is a separate proposal).

## 9. Dry-run semantics for custom commands

**Today `--dry-run` is plan-only.** `Run()` reports the plan and returns *before*
building any context or executing anything (`run.go:146-151`): no commands run,
no native handlers fire, no `${vars.*}` are captured. That is the right safety
default — a custom command could do anything — but it has two weaknesses for the
features above:

- The plan is rendered with **raw, un-interpolated** text, because interpolation
  only happens later in `runCommands` (`run.go:253`), which never runs in a dry
  run. A custom step shows `npm publish ${vars.npmOtp}` literally, and the new
  `${version.foo}` placeholders are invisible.
- A safe/read-only custom command (a linter, a `--dry-run`-aware publisher) can't
  *validate* during a dry run, even though running it would have no effect.

Note this is distinct from `--dry-build` (`ResolveOptions.DryBuild`,
`resolve.go:280`), which is a **real** run of the `build` step only.

### Part A — accurate preview (always on)

Build the **planned-version `ReleaseContext` even in `--dry-run`** (versions come
from the changeset set, not from manifest stamping — the same source §3's timing
guard already requires). The plan then renders custom commands with built-in vars
filled in: `${version.foo}`, `${versions}`, `${tags}`, `${changelog.foo}`.

Values that **cannot** exist without side effects render as a visible, masked
placeholder and are never resolved in a dry run:

- `${vars.*}` — resolving runs the capture command (e.g. `op item get … --otp`).
  Shown as `‹vars.npmOtp›`, never executed. (Today's eager-capture at
  `run.go:166-176` and per-command capture at `run.go:244-251` are both gated off
  in dry-run.)
- `${releaseUrl*}` — only exists after the forge `release` step runs. Shown as
  `‹releaseUrl›`.

So a dry run shows *exactly* what would run, fully interpolated where knowable,
with zero side effects and no secret commands invoked. This part is
non-negotiable and ships with the variables feature.

### Part B — opt-in execution (`dryRun` on a command)

For commands the user knows are safe, allow opting a step's action into actually
running during `--dry-run`, optionally as an alternate form:

```jsonc
"steps": {
  "publish": {
    "run": "npm publish",
    "dryRun": "npm publish --dry-run"   // runs THIS during --dry-run
  },
  "smoke": {
    "run": "./scripts/smoke.sh",
    "dryRun": true                       // run as-is during --dry-run (read-only)
  }
}
```

Semantics of the `dryRun` field (sibling of `run`, also valid on `before`/`after`
and global hooks):

- **absent** → listed only, not executed (status quo, safe default).
- **`true`** → the same command(s) execute during a dry run.
- **a command / list** → that alternate executes during a dry run instead.
- **`false`** → never execute *and* omit from the listing (explicitly hide).

`dryRun` is a `CommandList` (same ergonomic shapes as `run`) plus the `true`/
`false` booleans, so it maps 1:1 onto a multi-command `before`/`after`. Native
steps (`build`/`release`/`issues`) keep their existing dry behaviour — only the
command-bearing fields gain `dryRun`.

**Decision (locked 2026-06-15): ship both A and B.** Accurate preview *and* the
opt-in `dryRun` field land together — dry-run becomes a first-class validation
surface, not just a listing. This is the differentiator over knope/release-it,
which can only log custom commands in a dry run, never validate them.

## Config surface (after this change)

```jsonc
{
  "tool": "shiprig",
  "order": ["version", "commit", "build", "smoke-node", "publish", "tag", "push", "release"],
  "steps": {
    "smoke-node": {
      "name": "Node smoke test",        // §7 display name
      "ecosystems": ["node"],            // §8 targeting
      "run": "npm run smoke"             // custom command
    },
    "publish": { "confirm": true },
    "release": {
      "forge": "auto",
      "after": "slack-notify 'Shipped ${versions} — ${releaseUrls}'"  // §3 aggregate built-ins
    }
  },
  "hooks": {
    "after": "echo done: ${versions}",
    "onError": "./scripts/rollback.sh ${version.foo}"   // §3 addressed built-in
  }
}
```

## 10. Worked example — every lifecycle location

A complete, polyglot release that exercises **every** place a command can run.
The repo ships two packages and this run releases both:

| Package | Eco | Key | New version | Tag | Release URL (after `release`) |
|---------|-----|-----|-------------|-----|-------------------------------|
| `@acme/web` | node | `web` | `2.1.0` | `@acme/web@2.1.0` | `…/releases/tag/@acme/web@2.1.0` |
| `acme/cli` (Go module) | go | `cli` | `1.4.0` | `cli/v1.4.0` | `…/releases/tag/cli/v1.4.0` |

So the derived built-ins resolve to:

- `${versions}` → `@acme/web@2.1.0, acme/cli@1.4.0`
- `${version.web}` → `2.1.0`, `${version.cli}` → `1.4.0`
- `${version}` (bare) → **error** (2 packages — use `${version.<pkg>}`/`${versions}`)
- `${tags}` → `@acme/web@2.1.0, cli/v1.4.0`
- `${releaseUrls}` → both URLs (only after the `release` step runs)
- `${issues}` → `#231, #244` (parsed from released commits)

### 10.1 `.changeset/release.jsonc` (annotated)

```jsonc
{
  // Base tool for the built-in version/publish/tag steps.
  "tool": "shiprig",

  // Custom order: a "smoke-node" step is inserted after build, before publish.
  "order": ["version", "commit", "build", "smoke-node", "publish", "tag", "push", "release", "issues"],

  // Captured variables (§ vars). Eager ones run up front; lazy ones run on first
  // reference (so a fresh OTP is fetched at the publish step, not minutes earlier).
  "vars": {
    "buildId": { "command": "git rev-parse --short HEAD" },          // eager
    "npmOtp":  { "command": ["op", "item", "get", "npm", "--otp"], "lazy": true }
  },

  // GLOBAL HOOKS — bracket the whole run.
  "hooks": {
    "before":  "./scripts/preflight.sh ${buildId}",                   // once, before any step
    "after":   "slack-notify 'Shipped ${versions} (${releaseUrls})'", // once, after all steps OK
    "onError": "./scripts/rollback.sh"                                // on any failure, before abort
  },

  "steps": {
    // version — built-in command step. after-hook prints the planned versions.
    "version": {
      "after": "echo 'Planned: ${versions}'"
    },

    // commit — built-in, with a custom message template.
    "commit": { "message": "chore: release ${versions}" },

    // build — NATIVE step. Can't be replaced here; wrapped with before/after.
    "build": {
      "before": "npm run lint",            // PER-STEP BEFORE
      "after":  "echo built ${tags}"       // PER-STEP AFTER
    },

    // smoke-node — CUSTOM step (not a built-in). Node-only, with a display name
    // and a dry-run that actually runs (read-only).
    "smoke-node": {
      "name": "Node smoke test",           // §7 display name (shown in the UI)
      "ecosystems": ["node"],              // §8 targeting — skipped in a Go-only release
      "run": "npm run smoke -- --build ${buildId}",
      "dryRun": true                       // §9B — safe to run during --dry-run
    },

    // publish — built-in command step. Confirm gate, lazy OTP, and a real
    // --dry-run variant so dry runs validate the publish without shipping.
    "publish": {
      "confirm": "Publish ${versions} to the registries?",  // gate (after before-hooks)
      "args":   ["--otp", "${vars.npmOtp}"],                // appended to the built-in
      "dryRun": "shiprig publish --dry-run"                 // §9B — alternate command in dry-run
    },

    // push — built-in, gated.
    "push": { "confirm": true },

    // release — NATIVE forge step. after-hook posts the per-package URLs.
    "release": {
      "forge": "auto",
      "after": "./scripts/announce.sh ${releaseUrl.web} ${releaseUrl.cli}"
    }

    // issues — native, default config (no overrides).
  }
}
```

> **Native-step override (§6), shown separately** — replacing the forge release
> with your own command (emits a `log` note, native action skipped):
>
> ```jsonc
> "release": {
>   "run": "gh release create ${tag.web} --notes ${changelog.web}",
>   "dryRun": "echo would release ${tag.web}"
> }
> ```

### 10.2 Lifecycle order — real run (`shiprig release`)

Every location, in the exact order the engine fires it (`run.go:145-211`):

```
▶ plan rendered (all built-ins interpolated from planned versions)

1.  hooks.before ............ ./scripts/preflight.sh a1b9c2d        ← ${buildId} (eager) captured first
2.  eager vars .............. buildId already captured (cached)

── version ──
3.  version.action .......... shiprig version
4.  version.after ........... echo 'Planned: @acme/web@2.1.0, acme/cli@1.4.0'

── commit ──
5.  commit.action ........... git add -A && git commit -m 'chore: release @acme/web@2.1.0, acme/cli@1.4.0'

── build (native) ──
6.  build.before ............ npm run lint
7.  build.action ............ «native: build distributable artifacts»
8.  build.after ............. echo built @acme/web@2.1.0, cli/v1.4.0

── smoke-node (custom, node match ✓) ──
9.  smoke-node.action ....... npm run smoke -- --build a1b9c2d

── publish ──
10. publish.before .......... (none)
11. publish.CONFIRM ......... “Publish @acme/web@2.1.0, acme/cli@1.4.0 to the registries?”  [y/N]
12. npmOtp captured ......... op item get npm --otp        ← lazy, resolved here on first reference
13. publish.action .......... shiprig publish --no-git-tag --otp ‹masked›

── tag ──
14. tag.action .............. shiprig tag

── push ──
15. push.CONFIRM ............ “Proceed with the 'push' step?”  [y/N]
16. push.action ............. git push --follow-tags

── release (native) ──
17. release.action .......... «native: per-package forge release»   ← ${releaseUrl.*} now known
18. release.after ........... ./scripts/announce.sh https://…/@acme/web@2.1.0 https://…/cli/v1.4.0

── issues (native) ──
19. issues.action ........... «native: comment on / close #231, #244»

20. hooks.after ............. slack-notify 'Shipped @acme/web@2.1.0, acme/cli@1.4.0 (https://…, https://…)'

✔ release complete
```

On a failure at any numbered line, the engine runs `hooks.onError`
(`./scripts/rollback.sh`) and aborts — remaining lines do not run.

### 10.3 The same release under `--dry-run`

Part A (preview) interpolates everything knowable from **planned** versions;
Part B (`dryRun`) decides what actually executes. Placeholders `‹…›` mark values
that would require a side effect to know:

```
▶ DRY RUN — plan with interpolation; only dryRun-enabled commands execute

1.  hooks.before ............ ‹listed, not run›  ./scripts/preflight.sh ‹vars.buildId›
                              (no var capture in dry-run → buildId shown as placeholder)
── version ──
3.  version.action .......... ‹listed›  shiprig version
4.  version.after ........... ‹listed›  echo 'Planned: @acme/web@2.1.0, acme/cli@1.4.0'   ← planned versions ARE known
── commit ──
5.  commit.action ........... ‹listed›  git add -A && git commit -m 'chore: release @acme/web@2.1.0, acme/cli@1.4.0'
── build (native) ──
6.  build.before ............ ‹listed›  npm run lint
7.  build.action ............ ‹native, skipped in dry-run›
8.  build.after ............. ‹listed›  echo built @acme/web@2.1.0, cli/v1.4.0
── smoke-node (node ✓) ──
9.  smoke-node.action ....... ✔ EXECUTED  npm run smoke -- --build ‹vars.buildId›   ← dryRun:true
── publish ──
11. publish.CONFIRM ......... (gates are not prompted in dry-run)
13. publish.action .......... ✔ EXECUTED  shiprig publish --dry-run                 ← dryRun alternate (no OTP captured)
── tag / push ──
14/16. action ............... ‹listed›  shiprig tag  /  git push --follow-tags
── release (native) ──
17. release.action .......... ‹native, skipped›
18. release.after ........... ‹listed›  ./scripts/announce.sh ‹releaseUrl.web› ‹releaseUrl.cli›   ← URLs unknowable in dry-run
── issues ──
19. issues.action ........... ‹native, skipped›
20. hooks.after ............. ‹listed›  slack-notify 'Shipped @acme/web@2.1.0, acme/cli@1.4.0 (‹releaseUrls›)'

✔ dry run — 2 commands executed (smoke-node, publish --dry-run), 0 side effects
```

Key takeaways a user can see at a glance:

- **Planned versions/tags/changelog interpolate** even in dry-run; **`${vars.*}`
  and `${releaseUrl*}` show as `‹…›`** because resolving them has side effects.
- **`dryRun: true` / `dryRun: "alt"` commands actually run** (smoke-node, the
  publish preview); everything else is listed only.
- **Confirm gates and native actions don't fire** in dry-run.
- A Go-only release of the same config would show `smoke-node` as
  `‹skipped: no node packages in this release›`.

### 10.4 Verified — real `shiprig release --dry-run`

The traces above are illustrative. Below is **actual captured output** from the
built binary against a two-package Node fixture (`@acme/web@2.1.0`,
`@acme/api@1.4.0`) with this config:

```jsonc
"order": ["version", "smoke", "publish"],
"steps": {
  "smoke":   { "name": "Smoke test", "run": "echo smoke ${versions}", "dryRun": true },
  "publish": { "run": "echo would publish ${version.web}", "dryRun": "echo DRY-PUBLISH ${versions}" }
}
```

```text
Release plan (dry run - nothing will run):
  - version
      run: shiprig version
  - Smoke test
      echo smoke @acme/api@1.4.0, @acme/web@2.1.0
  - publish
      run: echo would publish 2.1.0

==> Smoke test
    $ echo smoke @acme/api@1.4.0, @acme/web@2.1.0
    smoke @acme/api@1.4.0, @acme/web@2.1.0
ok Smoke test
==> publish
    $ echo DRY-PUBLISH @acme/api@1.4.0, @acme/web@2.1.0
    DRY-PUBLISH @acme/api@1.4.0, @acme/web@2.1.0
ok publish
Release complete. dry run - preview only
```

Confirms, against the real engine: the `"smoke"` key renders as **Smoke test**;
`${versions}` interpolates (sorted); `${version.web}` → `2.1.0`; `version`
(built-in, no `dryRun`) is listed but not executed; `Smoke test` (`dryRun: true`)
runs; `publish` runs its **alternate** (`DRY-PUBLISH …`, not `would publish`).

## Implementation plan (ordered, each independently shippable)

> **Progress: all steps implemented** on branch `feat/release-vars-and-steps`
> (full `go build` + `go vet` + module tests green). Engine complete and tested
> behind a fake `ReleaseContext`; the host (`hostReleaseContext`) supplies
> versions, tags, **changelog notes** (`forge.Notes`), **forge release URLs**
> (`forge.ReleaseURL`, GitHub; "" on GitLab/Gitea), and **issue numbers**
> (`forge.ResolvedIssueNumbers`) end-to-end. Only `${issueBranch}` stays empty —
> shiprig has no issue-branch scheme in the release flow.

1. ✅ **§7 display name** — `StepConfig.Name`, `ResolvedStep.DisplayName` +
   `Label()`, rendered in all four UIs (plain/rich reporters, plan editor,
   dashboard) via an id→label map; tests added. *(Pure engine + reporters.)*
2. ✅ **§6 native override** — confirmed `run` already replaces a native step;
   added `ResolvedStep.OverridesNative` + a non-silent plan note (decision #3),
   tests, and `init` starter docs. *(Pure engine + reporters + docs.)*
3. ✅ **§8 ecosystem targeting** — `StepConfig.Ecosystems`,
   `ResolveOptions.Ecosystems`/`KnownEcosystems`, skip-with-reason (flows through
   existing `SkipReason` rendering), load-time validation; host computes the
   present/known sets lazily (only when a step opts in) via `ws.Discover` +
   registry; engine and host tests added.
4. **§1-§3 built-in variables** — the big one:
   a. ✅ `pipeline/context.go`: `ReleaseContext` + `ReleasePackage`/`IssueRef`,
      and a `releaseVars` resolver (addressed/aggregate/bare forms, issues,
      issueBranch).
   b. ✅ `New(...)` + `Run()` thread `relctx`; `runCommands` resolves release refs
      per command (so forge URLs reflect run progress) and fails the command on a
      usage error (ambiguous bare form, unknown package/issue).
   c. ✅ Bare-form ambiguity error; aggregate formatting; short-key addressing +
      full-name alias (§4). 16 engine tests behind a fake `ReleaseContext`.
   d. ✅ Host `ReleaseContext` in `cli/release.go` (`hostReleaseContext`): supplies
      Name/Key/Ecosystem/Version/Tag from `ws.Discover` (lazy, ignores respected)
      and **Changelog** via the exported `forge.Notes`.
   e. ✅ **Forge release URLs** — `forge.ReleaseURL` + a `Provider.ReleaseURLCmd`
      (gh `release view --json url`); `hostReleaseContext.ReleaseURL` fetches per
      package on demand and caches. GitHub resolves; GitLab/Gitea return "".
5. ✅ **§9 dry-run** — `Run` now routes `--dry-run` to `runDry`:
   a. ✅ **Part A:** `previewSteps`/`previewInterpolate` render the plan with
      built-in vars filled in; `${vars.*}` and `${releaseUrl*}` become `‹…›`
      placeholders (never captured/resolved); `${env.*}`/`${tool}` resolve.
   b. ✅ **Part B:** `StepConfig.DryRun` (`DryRunSpec`: bool ∪ command/list);
      `Resolve` derives `DryRunAction`/`DryRunHidden`; `runDryCommands` runs the
      opted-in commands without capturing vars; `false` hides the action. Native
      actions and confirm gates don't fire. (Scope note: `dryRun` governs the
      step **action**; before/after + global hooks are list-only in a dry run —
      a deliberate safe default, per-hook execution is a possible follow-up.)
6. ✅ **§5 issue values** — `forge.ResolvedIssueNumbers` parses the released
   commit messages (via `issuerefs`); `hostReleaseContext.Issues()` surfaces them
   so `${issues}` resolves. `${issueBranch}` intentionally stays empty (no
   issue-branch scheme in shiprig's release flow).

All steps land on `feat/release-vars-and-steps`. The only deliberate non-goal is
`${issueBranch}` (and `${releaseUrl}` on GitLab/Gitea), both noted above.

## Testing

- **Engine unit (`pipeline_test.go`, `config_test.go`, new `context_test.go`):**
  bare `${version}` single-package resolves; bare `${version}` multi-package
  errors with the guidance message; `${version.foo}` / `${versions}` /
  `${releaseUrl.foo}`; unknown `${version.bad}` errors; lazy resolution called at
  most once (cache); `run` overrides a native step; before/after still wrap a
  native step; `ecosystems` skip-reason when no matching package; display name in
  reporter output; unknown ecosystem rejected at load.
- **Fake `ReleaseContext`** in tests so the engine stays package-blind and
  host-free.
- **Host integration:** a polyglot fixture (Node + Go) asserting Node-targeted
  steps skip in a Go-only release and `${versions}` lists both in a mixed
  release; forge-URL capture covered against the `gh`/`glab` fakes already in
  `forge/*_test.go`.
- **Masking:** confirm built-in values are *not* masked while `${vars.*}` still
  are.
- **Dry-run (§9):** Part A — `--dry-run` renders built-in `${version.foo}`/
  `${versions}` interpolated, shows `${vars.*}`/`${releaseUrl}` as `‹…›`, and
  executes nothing / captures no vars (assert the capture runner is never
  called). Part B — `dryRun: true` runs the command in dry-run; `dryRun: "alt"`
  runs the alternate; `dryRun: false` hides it from the plan; absent ⇒ listed,
  not run.

## Decisions (locked 2026-06-15)

1. **Addressing key spelling (§4): short name + full alias.** `${version.foo}`
   (last path segment) is primary; the full manifest name (`${version.@scope/foo}`)
   is accepted as an exact alias. Collisions fall back to ecosystem-qualified
   keys (`node:foo`) for the colliding packages only, with one `log` note.
2. **Bare-form in multi-package: always hard error.** No opt-out flag. A bare
   `${version}`/`${changelog}`/`${releaseUrl}`/`${tag}` in a multi-package release
   fails with guidance pointing at the addressed and aggregate forms. Bare form
   works only when exactly one package is released. (Matches the competitors'
   safety floor; the addressed/aggregate forms remove any need for a guessed
   value.)
3. **Native override (§6.4): `log`-level note, no hard stop.** When a custom
   `run` replaces a native step, proceed and emit one note (e.g. `release: using
   custom run, native forge release skipped`). No confirm gate, no error.
4. **Ecosystem mismatch (§8): skip with reason.** A step targeting an ecosystem
   absent from the release stays in the plan, skipped with `no <eco> packages in
   this release` — never a hard error. Keeps one polyglot config portable across
   single-ecosystem releases.
5. **Dry-run (§9): preview + opt-in execution.** Part A (accurate interpolated
   preview, no side effects, `${vars.*}`/`${releaseUrl}` shown as placeholders)
   **and** Part B (a `dryRun` field on `run`/`before`/`after`/hooks: `true`,
   `false`, or an alternate command) both ship in this change.

### Still open (not blocking — settle during implementation)

- **`${releaseUrl}` cost.** Capturing URLs may need an extra `release view` per
  package on forges whose `create` output omits the URL (`forge/forge.go:141`).
  Lean: acceptable, and only incurred when `${releaseUrl*}` is actually
  referenced (lazy), so the default path pays nothing.
```
