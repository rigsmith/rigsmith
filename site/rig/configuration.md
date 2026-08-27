# Configuration

rig is convention-first — it works with **zero configuration**. An optional
`.rig.json` at the repo root (found by walking up from cwd; the root anchor
precedence is `.rig.json` > solution/workspace manifest > git root) supplies
only what can't be inferred.

It's **JSONC** (comments + trailing commas welcome); a missing file is fine;
unknown keys get a did-you-mean warning.

```jsonc
{
  "defaultProject": "MyApp",
  "quiet": false,
  "exclude": ["*.Bench", "*.Demo"],
  "env": { "MYAPP_LOG": "1" },        // layered over .env/.env.local + ambient
  "coverage": { "min": 80, "open": true },
  "publish": { "rid": "linux-x64", "selfContained": true },
  "commands": {
    "deploy": "./deploy.sh && echo done",          // shell string (portable shell)
    "bench": ["go", "test", "-bench", "."],        // argv (exec'd directly)
    "clean": { "script": "rm(`-rf`, `dist`); mkdir(`-p`, `dist`)" },  // tengo
    "native": { "command": "sed -i s/a/b/ f", "shell": "system" }     // OS shell
  }
}
```

| Key | Type | Meaning |
|-----|------|---------|
| `defaultProject` | string | Project to act on when several are runnable (settable via the default-setter) |
| `solution` / `test.project` | string | Pin the .NET solution / test project |
| `ecosystem` | string | Pin the primary ecosystem (`dotnet`/`node`/`go`/`cargo`) when detection is ambiguous |
| `quiet` | bool | Suppress the `→ command` echo (same as `--quiet`) |
| `exclude` | []string (globs) | Hide projects from discovery/pickers (also written by the picker's `x` key) |
| `env` | map | Extra environment; layered file (`.env`/`.env.local`) < ambient < config < command |
| `coverage.*` / `publish.*` / `rebuild.skip` / `kill.match` | — | Verb defaults (flags win) |
| `artifacts` | map | What this repo builds and what each thing is built from, for [`rig verify`](./verbs#verify)'s agreement check ([see below](#artifacts)) |
| `verify.run` / `verify.runTimeout` | bool / duration | Whether `rig verify` includes the run step, and how long it must stay alive to count as started (default `10s`) |
| `worktree.autoOpen` / `worktree.openCmd` | bool / string | Whether `rig worktree new` opens a review window, and the command to open it ([see below](#worktree)) |
| `commands` | map | Custom verbs: shell string, argv array, Tengo `script`, or object with per-OS (`macos`/`windows`/`linux`), `env`, `cwd`, `description`, `shell` ([see below](#commands)) |
| `shell` | string | How a shell-string command runs: `portable` (default, cross-platform) or `system` (the OS shell). A command's own `shell` overrides it |
| `aliases` / `dotnet.*` | — | Aliases; `dotnet`-namespaced keys fold over legacy top-level |

Custom commands honor `--dry-run`; extra args are forwarded. A custom name that
collides with a built-in verb never runs — the built-in wins — and rig says so
on load rather than ignoring it silently:

```
rig: "build" in /repo/.rig.json is a built-in rig verb, so that entry never runs
     — rename it (e.g. "build:custom")
```

Config writes (e.g. the default-setter and the picker's exclude/include keys)
preserve comments via the JSONC editor.

Whatever a command resolves to — after OS selection, shell choice, `cwd` and the
env layers — is printable with [`rig explain <verb>`](./verbs#explain), which
runs nothing.

## Custom commands {#commands}

A `commands` entry adds a `rig <name>` verb. There are three forms:

```jsonc
"commands": {
  // 1. Shell string — runs cross-platform through an in-process portable
  //    shell, so pipes, &&/||, $VAR, globbing, and cp/mv/rm/mkdir all work the
  //    same on Linux, macOS, and Windows. No per-OS variants needed.
  "lint": "eslint . && prettier --check .",

  // 2. Argv array — exec'd directly, no shell, no quoting hazards.
  "bench": ["go", "test", "-bench", ".", "./..."],

  // 3. Tengo script — a cross-platform command body with real logic.
  "release": { "script": [
    "mkdir(`-p`, `dist`)",
    "if ctx.ecosystem == `go` { sh(`go build -o dist/app ./...`) }",
    "sh(`tar czf dist/app.tgz -C dist app`)"
  ]}
}
```

The object form takes `description`, `env`, `cwd` (relative to the repo root),
`shell`, and either `command` (string/argv) **or** `script` — not both.

### Cross-platform by default {#shell-mode}

Shell-string commands run through the **portable shell** by default — the same
mvdan.cc/sh interpreter shiprig uses — so one command line works on every OS.
Opt a command (or the whole config) back into the OS shell with `"shell":
"system"` when you need a real userland (`sed`, `awk`) or OS-specific syntax:

```jsonc
{
  "shell": "system",                                  // default for all commands
  "commands": {
    "fmt": { "command": "gofmt -w .", "shell": "portable" }  // …but this one is portable
  }
}
```

Argv-form commands are unaffected (always exec'd directly).

### Tengo scripts {#script}

The `script` form runs through rig's embedded [Tengo](https://github.com/d5/tengo)
runtime — the same engine as shiprig's release scripts (see
[Tengo in 5 minutes](https://github.com/rigsmith/rigsmith/blob/main/docs/TENGO-FOR-JS-DEVS.md)).
Write it as a string, an array of lines, or `{ "file": "./scripts/x.tengo" }`
(resolved relative to the config — a `.tengo` extension is the convention, and
any other is flagged as a likely typo but still loads). Tip: use backtick (raw)
string literals so they don't collide with JSON's double quotes.

**Builtins** (side effects honor `--dry-run` — previewed, not performed):

| Builtin | Does |
|---------|------|
| `sh(cmd)` | Run a shell command (portable by default; `shell:"system"` for the OS shell), return its stdout. A non-zero exit aborts the script |
| `cp` / `mv` / `rm` / `mkdir` | Cross-platform file ops (`cp -r`, `rm -rf`, `mkdir -p`) |
| `log(...)` | Print a line |
| `fail(msg)` | Abort the command with an error |

**`ctx`** exposes the invocation:

| Field | Value |
|-------|-------|
| `ctx.args` | Extra CLI args (`rig release v2` → `ctx.args[0] == "v2"`) |
| `ctx.env` | The layered environment as a map |
| `ctx.root` | Repo root |
| `ctx.cwd` | Working directory (the command's `cwd`, else root) |
| `ctx.ecosystem` | Resolved ecosystem (`dotnet`/`node`/`go`/`cargo`, or pinned) |
| `ctx.os` | `darwin` / `linux` / `windows` |

## Excluding projects {#exclude}

`exclude` hides projects from discovery and the pickers. You can edit it by hand
or let the [`run`/`ui` picker](./verbs#picker-controls-exclude-include) write it
for you (`x` to exclude, `i` to show/re-include). Globs match against each
project's full name, short name, and repo-relative path:

```jsonc
{ "exclude": ["*.Bench", "*.Demo", "examples/*"] }
```

Paths are as valid as names, and matter when two copies of a project share one
name — a name glob would hide both, a path glob hides the copy you don't want:

```jsonc
{ "exclude": ["build/staging/MyApp"] }
```

## Nested worktrees {#nested-worktrees}

A git worktree checked out *inside* the repo (`.claude/worktrees/<branch>`,
`wt/<branch>`, a hand-made `git worktree add ./tmp`) holds a full copy of every
project. rig skips those copies during discovery, so they can't shadow the real
ones: without that, `rig run <name>` goes ambiguous and a `defaultProject` can
resolve to a stale checkout that looks exactly like your app.

Pass `--include-worktrees` for the rare cross-worktree sweep. `rig info` names
any nested worktree it is holding back — and says when `rig prune` would already
remove it, because its branch is merged. Sibling worktrees (`rig worktree new`'s
`<repo>-worktrees/<branch>`) live outside the repo and were never discovered.

## Worktrees {#worktree}

`rig worktree new` can open the new sibling checkout in a separate review window.
Both *whether* it opens and *what* opens it are configurable:

```jsonc
{
  "worktree": {
    "autoOpen": true,          // default false; --open / --no-open override per run
    "openCmd": "cursor -n"     // default "code -n"; e.g. "subl -n", "idea"
  }
}
```

The worktree path is appended as the final argument to `openCmd` and run
directly (no shell). When the opener isn't on `PATH`, rig prints the command to
run instead.

## Stack workspaces {#stack}

A [stack workspace](./stack) is described by `rig.stack.jsonc` at its root
(or a `stack` key in `.rig.json`). Each entry names a repo fused into this
history: the key is the directory it lives under.

```jsonc
{
  "branchPrefix": "stack/",                   // prepended to `send` branch names
  "repos": {
    "pty-core": {
      "upstream": "github.com/acme/pty-core",   // where PRs go
      "fork": "github.com/you/pty-core",        // where `send` pushes
      "upstreamBranch": "main"                  // branch of upstream to follow
    }
  },
  // Written by import and pull, not by hand: the full SHA each repo last took.
  "lastSync": { "pty-core": "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678" }
}
```

Repo specs are `host/owner/name` — no scheme and no `.git`, since the same spec
has to serve as a URL, an engine path, and a label.

`upstreamBranch` names the branch of `upstream` this directory follows — what
`pull` takes and what `send` roots on. It is deliberately not called `branch`:
that would collide with `rig stack send <repo> <new-branch>`, where the branch
is one you are creating on your fork for a single change. A manifest written
with the older `branch` key is still read.

Instead of a branch, a directory can be **pinned** with `upstreamTag` or
`upstreamCommit`. `pull` then has nothing to do until you edit the pin, and
`status` says `pinned to tag v1.4.2` rather than `up to date`, because the two
mean very different things. A tag is resolved once, when the pin is
first taken, and the result is recorded in `lastPin` beside the cursor. An
upstream that later force-moves or re-cuts that tag does not move your
workspace; editing the pin does, because that changes what was recorded. To
follow a moved tag deliberately, `rig stack pull --repin`.

Repinning works in both directions. Moving a project to an *older* revision
cannot be done by merging, since the target is already an ancestor of what the
workspace holds, so the directory is replaced with the pinned revision instead
and `pull` reports it as `moved`. That discards what is under it, so it is
refused when the directory holds changes of its own: send or revert them first.

Reach for a pin when what depends on the library needs an older release than
upstream's tip. Fusing at the tip in that situation gives you sources that no
longer compile against your consumer — the API it uses has moved — and the fix
is not the build overlay, it is fusing the right point in the first place.

```jsonc
"some-lib": {
  "upstream": "github.com/acme/some-lib",
  "fork": "github.com/you/some-lib",
  "upstreamTag": "v1.4.2"          // or "upstreamCommit": "<full 40-char sha>"
}
```

Exactly one of `upstreamBranch`, `upstreamTag` and `upstreamCommit` may be set;
two would mean guessing which wins, and the guess would be invisible in the
fused history afterwards. `upstreamCommit` must be the full 40-character SHA,
since resolving an abbreviation requires fetching the repository — which is the
thing the pin exists to decide. Annotated tags are peeled to the commit they
point at.

`lastSync` is a separate map rather than a field per repo because it is machine
written: an import or pull rewrites that one value while the entries you wrote,
and their comments, stay untouched. The values are full 40-character SHAs — an
abbreviated one would never equal the tip `status` reads from the remote.

`branchPrefix` is prepended to the name you give `rig stack send`, so
`send pty-core read-timeout` creates `stack/read-timeout` on your fork. It
defaults to `stack/`, keeping these branches apart from your own work on the
same fork; set it to `""` for bare names, or override it on a single repo whose
upstream asks for a different convention. A name that already carries the prefix
is left as it is.

An optional `"josh"` key pins the version of [josh](https://josh-project.dev) —
the history-filtering engine [`init` and `pull`](./stack) run — for this
workspace, overriding the one rig ships with. Changing it against existing
workspace history is at your own risk.

## Declaring artifacts {#artifacts}

[`rig verify`](./verbs#verify) infers the common case with no configuration at
all. The `artifacts` block is for what it *cannot* know: generated resources,
multi-artifact builds, an output tree beside the repo. Each entry names
something built and the globs it is built from — anything under `path` older
than the newest matching input is stale.

```jsonc
{
  "artifacts": {
    // Anything here that is older than its newest input is stale.
    "browser":    { "path": "../out/Component_arm64/Sheepish.app",
                    "inputs": ["**/*.cc", "**/*.grd", "**/*.gni"] },
    "unit-tests": { "path": "../out/Component_arm64/brave_unit_tests",
                    "inputs": ["**/*.cc", "**/*.h"] },
    // Shorthand: the path alone. Enough to report "never built", not enough to
    // compare — verify says the comparison was skipped rather than passing it.
    "installer": "dist/Setup.exe"
  },
  "verify": {
    "run": true,           // default; false drops the run step from the sequence
    "runTimeout": "30s"    // default 10s — how long "it starts" gets to prove itself
  }
}
```

- `path` is a file **or a directory**, relative to the repo root or absolute; it
  may sit outside the repo (`../out/…`). A directory is judged by its oldest
  file, which is what catches a fresh bundle holding a stale resource.
- `inputs` are globs relative to the repo root. `*` and `?` stay inside a path
  segment; `**` spans directories, so `**/*.cc` matches at any depth. Matching
  is case-insensitive, and skips the usual noise (`node_modules`, `bin`, `obj`,
  `target`, `dist`, `.git`) plus anything `.gitignore`d.

## .NET repository discovery

rig detects .NET repos even when there's no solution or `.csproj` at the root, by
recognizing conventional markers (`Directory.Build.props`,
`Directory.Build.targets`, `Directory.Packages.props`, `global.json`,
`nuget.config`). Projects can live in subdirectories — `rig run` / `build` /
`test` discover them and offer a subproject picker even when no single primary
ecosystem resolves.

## Embedding shiprig / changerig config {#embedded}

`.rig.json` can also carry a sibling tool's config as a top-level key, so a repo
that prefers one file can keep everything here instead of in `.changeset/`:

- `"shiprig"` (or `"release"`) — the [release pipeline](/shiprig/pipeline) config
- `"changerig"` (or `"changeset"`) — the [changeset](/changerig/lifecycle) config

Each tool also still reads its standalone files; provide the config in **exactly
one** place — if a tool finds it both here and in a standalone file, it stops and
lists the conflict rather than guessing.
