# rig

rigsmith's convention-first dev launcher — the Go successor to the .NET/Node
`rig`. The same verb works in any ecosystem; rig detects the repo and runs the
right native command.

```sh
rig info                 # what rig discovered (config, dev commands, packages)
rig ui                   # interactive menu over the dev verbs
rig build                # → go build ./...  | dotnet build | npm run build
rig test
rig run
rig format
rig lint
rig typecheck
rig build --dry-run      # print the command, don't run it
rig build --quiet        # suppress the → command echo
```

Beyond the dev loop it manages parallel worktrees, and **stackspaces** —
your app and the libraries you have forked, fused into one history, so a change
can span them in a single commit while each still leaves as itself. Private
repos need no setup beyond `gh auth login`:

```sh
rig stack setup                      # a fresh clone: engine, members, overlay, status
rig stack init                       # scaffold the manifest; run again to import
rig stack add github.com/them/lib    # fuse another repo
rig stack rm some-lib                # take one back out: manifest, tree and overlay
rig stack status                     # who has moved upstream, what has not left, and where each topic's PR is
rig stack pull some-lib              # take that movement
rig stack propose some-lib read-timeout -m "Fix the read timeout"   # ALL of some-lib's changes → stack/read-timeout
rig stack propose some-lib read-timeout --from read-timeout         # ...or one topic's tree (stack-pr-read-timeout); root it on the import, or it carries its parent's fixes too
rig stack propose some-lib --track-only        # refresh trackBranch after pull; no proposal or PR
rig stack push my-app                # a repo you own: its own branch, history intact
rig stack edge pack lib --number 42   # reconstruct trackBranch; rename, version, pack and verify
rig stack edge status lib            # carried fixes, freshness and retirement evidence
rig stack edge init lib              # check/repair setup; each outward action confirms
rig stack doctor --edge              # same edge setup facts, read-only
rig stack edge ci --seed acme/stackspace@main --member lib --number 42 --report edge.json
rig stack pack some-lib              # its packages, built here — a bare checkout of a proposed branch cannot
rig stack propose some-lib read-timeout --dry-run   # the commit and fork branch that would go; touches no remote
rig stack push my-app --dry-run      # the target, branch and commits that would go; touches no remote
rig stack seed ../rigstack-acme      # portable seed; the menu suggests a path outside enclosing repos
```

Owned-member push compares content with upstream, so squash/rebase merges already
containing the work leave nothing to push. New commits remain separate on a safe
upstream base; ambiguous history is refused. Push verifies fast-forward ancestry locally
and never forces an update. `--dry-run` uses the same delivery plan.

`rig stack pull -n` previews cursor and upstream movement without changing the stackspace. Setup operations (`init`, `add`, `rm`, `setup`) and offline seed directory exports refuse dry-run; seed publication previews without cloning or pushing; `wire` and `doctor --fix` report without writing. `ci`, `ci-guard`, and `deps` (including `--publish`) refuse dry-run; `home` is read-only.

`rig stack edge pack <member>` reads the committed stack manifest by default,
or `--manifest` / `--seed`, and packs a temporary reconstruction of the fork
trackBranch. `--upstream-tip` merges onto current upstream; conflicts report
inconclusive with a remedy and skip packing, clearing stale top-level package files and writing `edge-pack.json`
with `status: "inconclusive"`. Cleanup preserves other files and all directories. With `-n`, it prints the outcome without changing output. `-o` chooses the output directory
(default `dist/`); `edge-pack.json` records verification expectations and provenance.
`--number` defaults to unpublishable 0; `--here` always marks output unpublishable.
`rig stack edge status [member]` reports carried fixes, freshness, newest edge
version and retirement evidence; `--json` emits its report. `rig stack released`
previews migration to public ids, refusing plans that leave another republished id
from the same member behind. Pack does not push a feed.
`rig stack edge ci --seed acme/stackspace@main --member lib --number 42 --report edge.json`
reconstructs, wires, builds, tests and packs without credentials. A fresh
`--number 42 --publish edge.json` job proves ids, version, feed and fork ancestry, verifies
packages and hashes, checks monotonicity and pushes over HTTP using the environment
key named by `edge.pushKeySecret`. Require the same trusted `--number` in both
jobs. Configure `edge-publish` with a deployment-branch rule limited to the default
branch; store the key as an environment secret and remove any repository-secret
copy. Packed ids must be a non-empty subset of the publisher’s text-only allowed
ids; its own NuGet sibling ids enforce cross-member refusals. Third-party dependency and assembly-name checks
use the build report’s evaluation claims. `--upstream-tip` conflicts exit 0 with
an inconclusive report; publishing a tip requires `--allow-tip-publish`.
See `examples/github-workflows/stack-edge.yml`.
See [Edge builds](../../site/rig/stack.md#edge-builds).

`rig stack edge init [member]` reads the hand-authored manifest, checks publishing
coverage, trackBranch and wiring, and runs a real temporary pack with number 0.
It writes the root `.github/workflows/stack-edge.yml`, commits only that path,
and runs `rig stack seed --publish` to the configured home, with separate
terminal confirmations. It never creates the home or writes inside members;
commit other root edits before publication. It can create an explicit missing
`publishFrom` repo and disable each non-edge workflow in a publishing fork.
It creates or updates `edge-publish` to allow only the default branch, reads the
rule back, then sets the missing environment secret via stdin or `--push-auth`.
It never overwrites existing secrets; repository copies are reported for manual
deletion. Secret setting retries only 5xx, up to three attempts, with metadata
read-back and subprocess output withheld. Select a reviewed rig release in the
generated template. `rig stack doctor --edge` checks the same facts without
repair; `-n` skips the real pack and prints plans. Neither offers `--yes`.

On .NET, `rig stack wire` emits source signals for swapped siblings and their
transitive PackageReference closure, propagated through evaluated ProjectReferences,
keeping unreachable siblings absent.
See [source signals](../../site/rig/stack.md#source-signals-from-the-net-overlay) for static package-condition
and preview limitations. SDK changes that erase reference candidates fail with
`RIGSTACK001` instead of silently dropping guarded code or tests.

For GitHub Actions, use `rigsmith/stack-action@fcae919e5eb7036b81b94ad2b5977d5a77648128 # v1.0.0`
(full SHA recommended; `@v1` is the moving major tag).

`rig stack ci` requires exactly one of `--seed acme/rigstack-app@main` or
`--manifest stack/rig.stack.jsonc` and builds a member checkout with sibling sources in a temporary
stackspace. Its action verifies release downloads and reports trusted history
cache reuse and commit/filter provenance; `cache-history: 'false'` disables
bundle reuse. See [stack CI](../../site/rig/stack.md#testing-a-member-pr-in-its-stackspace) for the read-only,
no-secret fork PR setup and cache trust limits.

`propose --from` requires its topic to be an ancestor of HEAD; check out the
integration line containing it first. `propose --track-only` requires trackBranch,
a current cursor, a clean prefix and every recorded local topic in HEAD. It
refreshes only the integration snapshot and supports `--dry-run`.

`propose` proposes one squashed commit to a fork you contribute to; `push`
fast-forwards a project you own with every commit that touched it. Mark a
project `"owned": true` in the manifest to enable the second.

`rig stack deps` checks topic PR dependencies against upstream merge states and
manifest cursors; `rig stack status` shows the same chain. See the
[stack dependency guide](../../site/rig/stack.md#stack-topic-dependencies) for
recording edges, publishing statuses, and enabling scheduled refresh.

`rig stack released app lib --package Acme.Lib` verifies NuGet commit ancestry
and previews package pin and simple stack guard edits. `--apply` requires a clean
tree and interactive confirmation; `-n` previews. See [stackspaces](../../site/rig/stack.md).

With `publishPrefix` or `publishesAs`, select the original public `--package` id.
Released renames the consumer's republished reference and bumps its version,
using that referenced id for guard and pin checks. It refuses plans that would
leave other republished ids of the sibling behind.

Scheduled NuGet release migration uses `rig stack released --report` and a
separate `--publish` job to open or update a reviewed back-to-packages PR on the
member. The publisher reloads a trusted `--seed`, re-verifies ancestry/delivery
and derives edits from current default-branch blobs without running repo code.
Copy `examples/github-workflows/stack-released.yml`; repeat trusted package,
feed, version and warning selections in both jobs. npm and deeper transitive
chains remain unsupported. See [the stack guide](../../site/rig/stack.md#scheduled-return-to-packages) for permissions and review limits.

Transitive package migrations verify a published delivery dependency with
`--via-package`/`--via-version`. Use `rig stack status --releases` to opt into
release discovery; ordinary status performs no additional registry checks.

The default manifest home is a small seed repo shared by local setup and CI.
First publication to an existing seed requires matching root files. If they
differ, set up from that seed in a fresh directory and reconcile the root edits.
See [Stackspaces](../../site/rig/stack.md) for publishing, setup, member pointers, early `RigStackAvailable_<id>` availability properties, source build signals, evaluation timing, and the observed CI warning guard (`rig stack ci-guard <project>`).

Scheduled upstream checks: `rig stack canary` tests tips and reports cursor PRs
or failure issues through separate build and publish jobs. See the
[stack guide](https://rigsmith.dev/rig/stack).

`rig stack ci --topic <name>` tests all recorded topic PR heads together; see [stack CI](https://rigsmith.dev/rig/stack#testing-a-complete-topic).

### Adopting an upstream PR

`rig stack edge adopt lib https://github.com/acme/Lib/pull/412` creates
`stack-pr-adopt-lib-412` on the member's import commit, merging the PR's change onto
the cursor. It requires the member's tracked upstream branch as the PR base before
fetching, then fetches only the PR head from its head repository.
Conflicts and different existing topic content refuse; identical content is a
no-op. `--dry-run` writes no branch; member names complete. Merge the topic into
the integration line, add the exact printed entry to `edge.lib.adopted`, then run
`rig stack propose lib --track-only`. Adopt never writes the manifest or pushes.
Status and CI reports warn on moved PR heads in text and JSON while retaining pinned content
and release/retirement verdicts. See
[adopting a PR](../../site/rig/stack.md#adopt-someone-elses-pr).

## Verbs

| Verb | What |
|------|------|
| `build` | Build the project |
| `test` | Run the tests |
| `run` | Run the project |
| `format` | Format the code |
| `lint` | Lint the code |
| `typecheck` | Type-check the code |
| `verify` | `build` → `test` → `run` in sequence, then check the artifacts were built together (`--stale-only` checks only, `--no-run` skips the launch) |
| `coverage` | Run tests with coverage; `--min` gate; `--open` report (in-process cobertura→HTML for .NET) |
| `kill` | Kill dev processes by project/pattern/`--port` (config `kill.match` wins) |
| `add` / `remove` / `outdated` / `upgrade` | Package management, native per ecosystem |
| `install` / `ci` / `clean` / `rebuild` | Restore/clean/rebuild (rebuild scopes bin/obj removal on .NET) |
| `global` / `dlx` | Global tool install / one-shot tool run (`dnx`, `pnpm dlx`, …) |
| `publish` | dotnet publish with rid/output/self-contained from flags or `.rig.json publish.*` |
| `doctor` | Environment checklist (SDK pins via nearest `global.json`) |
| `cd` | Fuzzy project navigation (prints the dir; pair with a shell wrapper) |
| `watch <verb>` / `rig w r` | Watch modifier via the pre-parse pipeline (verb prefixes work too: `rig cove`) |
| `worktree` / `wt` | Parallel-dev worktrees (`new`/`list`/`open`/`rm`/`prune`) + pin the active `-dev` route (`use`/`active`/`unset`) |
| `branch` / `br` | Local branches (`list`/`rm`/`prune`, `--gone`) |
| `prune` / `tidy` | One sweep: reap merged worktrees then their branches |
| `init` | Scaffold a `.rig.json` |
| `config` | `get` / `set` / `path` / `edit` the `.rig.json` (comment-preserving writes) |
| `info` | Show what rig discovered (root, primary ecosystem, `.rig.json`, per-ecosystem dev commands, packages) |
| `ui` | Interactive bubbletea menu over the dev verbs (capability-gated) |
| *custom* | Any `commands` entry in `.rig.json` becomes a subcommand |
| *scripts* | In a Node repo, every package.json script becomes a verb |

The dev verbs map through each ecosystem's `EcosystemInfo.DevCommands` (shared
with shipRig), so an ecosystem declares its own commands. Ecosystems that don't
define `lint`/`typecheck` report "no mapping" cleanly.

Global flags: `--dry-run`/`-n` (print what would run, don't run it) and
`--quiet`/`-q` (suppress the `→ command` echo).

## Configuration (`.rig.json`, all optional)

rig is convention-first — it works with **zero configuration**. An optional
`.rig.json` at the repo root (found by walking up from cwd; the root anchor
precedence is `.rig.json` > solution/stackspace manifest > git root) supplies
only what can't be inferred. **JSONC** (comments + trailing commas welcome);
a missing file is fine; unknown keys get a did-you-mean warning.

```jsonc
{
  "defaultProject": "MyApp",
  "quiet": false,
  "exclude": ["*.Bench", "*.Demo"],
  "env": { "MYAPP_LOG": "1" },        // layered over .env/.env.local + ambient
  "coverage": { "min": 80, "open": true },
  "publish": { "rid": "linux-x64", "selfContained": true },
  "commands": {
    "deploy": "./deploy.sh",                       // shell string
    "bench": ["go", "test", "-bench", "."],        // argv
    "open": { "os": { "macos": "open .", "windows": "explorer ." } }
  }
}
```

| Key | Type | Meaning |
|-----|------|---------|
| `defaultProject` | string | Project to act on when several are runnable (settable via the default-setter) |
| `solution` / `test.project` | string | Pin the .NET solution / test project |
| `quiet` | bool | Suppress the `→ command` echo (same as `--quiet`) |
| `exclude` | []string (globs) | Hide projects from discovery/pickers |
| `env` | map | Extra environment; layered file (`.env`/`.env.local`) < ambient < config < command |
| `coverage.*` / `publish.*` / `rebuild.skip` / `kill.match` | — | Verb defaults (flags win) |
| `artifacts` / `verify.*` | map | What this repo builds and what it's built from (for `verify`'s staleness check); whether `verify` runs the app and for how long |
| `commands` | map | Custom verbs: shell string, argv array, or object with per-OS (`macos`/`windows`/`linux`), `env`, `cwd`, `description` |
| `aliases` / `dotnet.*` | — | Aliases; `dotnet`-namespaced keys fold over legacy top-level |

Custom commands honor `--dry-run`; extra args are forwarded. A custom name that
collides with a built-in verb is ignored. Config writes (e.g. the
default-setter) preserve comments via the JSONC editor.

## Roadmap

The remaining ergonomics tail (`[suggest]` completion, menu project-pickers,
`setup`/`self-update`, the interactive `default` verb, test-class fuzzy match)
is tracked in [../docs/FEATURE-PARITY.md](../docs/FEATURE-PARITY.md).
