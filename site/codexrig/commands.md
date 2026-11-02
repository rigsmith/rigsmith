# codexRig commands

## The lifecycle

| Command | What it does |
|---|---|
| `codexrig init` | Write the config, pick a private remote, install the hooks. |
| `codexrig sync` | Capture this machine's Codex setup and push it. |
| `codexrig pull` | Bring down what another machine synced. |
| `codexrig restore` | Write the synced setup onto this machine. |
| `codexrig status` | Where this machine stands. `--json` for scripts. |
| `codexrig doctor` | Check it is actually working, including user and installed project hook trust. `--fix` repairs what it can. |

`sync` takes `--dry-run` to capture without publishing, `--hook` to mark a
hook-driven run (which debounces, and never blocks), and `--flush` to capture the
rollout named on stdin immediately.

`restore` takes `--dir` to unpack somewhere harmless first, `--force` to write
over an established machine without asking, and `--prune` to remove skills,
prompts and rules that were deleted elsewhere.

## Accounts

| Command | What it does |
|---|---|
| `codexrig account add` | Track the login you are signed in as. |
| `codexrig account list` | What is tracked, and which is live. `--json`. |
| `codexrig account run [ref]` | Start Codex under one account, in its own home. |
| `codexrig account prepare [ref]` | Ready that home and print its path, without launching. |
| `codexrig account switch [ref]` | Change which login plain `codex` uses. |
| `codexrig account alias <ref> <name>` | Give an account a short name. |
| `codexrig account disable <ref>` / `enable <ref>` | Hold it out of automatic selection, and put it back. |
| `codexrig account remove <ref>` | Forget an account and delete its isolated home. Does not log it out. |
| `codexrig account purge` | Forget every account. Does not log any of them out. |
| `codexrig account doctor` | Check the live login and codexrig's record agree. |
| `codexrig account sessions` | Which Codex processes are running. |
| `codexrig account map [ref] [dir]` | Bind a directory to an account. |
| `codexrig account unmap [dir]` | Remove that binding. |

A bound directory answers a bare reference, and the nearest binding wins — so one
binding on a repository covers every worktree under it. With several accounts and
no binding, the refusal is `unmapped-directory`, which is something a launcher can
act on rather than a generic failure.

`prepare` is the machine-readable half of `run`: stdout carries the directory and
nothing else, so it can be captured directly.

```sh
CODEX_HOME=$(codexrig account prepare work) codex
```

With `--json` it emits one object including a stable `reason` when it refuses, so
another program can tell "no such account" from "that account cannot
authenticate".

## Hooks

| Command | What it does |
|---|---|
| `codexrig global install` | The backup hooks, in your own Codex home. |
| `codexrig global trust [--json]` | Trust exact current codexrig commands from your Codex home's `hooks.json`. |
| `codexrig global status` | Installed? Up to date? Trusted? |
| `codexrig project install` | The branch guard, in this repository's `.codex/`. |
| `codexrig project trust [--json]` | Trust exact current codexrig commands from this repository's `.codex/hooks.json`. |

Install and trust are separate because they are different acts: one writes a
file, the other tells Codex to trust a file. `trust` asks Codex for the hash
rather than computing one, and makes the edit through Codex so `config.toml`
keeps its own formatting.

Trust is a security boundary. Global scope may approve only `SessionStart:
codexrig pull` and `Stop: codexrig sync --hook` in the selected Codex home's
`hooks.json`. Project scope may approve only `PreToolUse: codexrig guard`, with
the current `guard.Matcher()` tool list, in this repository's `.codex/hooks.json`.
A backup command in a project file, or a guard command in the global file, is a
lookalike for that scope. A marker substring never grants approval.

Commands, case-sensitive events, matcher, handler type, and all hook fields must
match what that scope's installer writes. Unknown fields, `commandWindows`,
explicit timeout/status-message overrides, and other execution options fail
closed even when Codex omits them from its listing. Project trust, install and uninstall
refuse hooks resolving outside the resolved repo or to the global hooks file.
Symlinked repo roots and macOS `/var` aliases remain supported. Parent traversal
and ambiguous literal-backslash paths are refused.

Trust reports approvals, failures and unlisted handlers. Exact healthy hooks
from the other scope are omitted from text and retained under JSON `outside`. Human output escapes control characters in hook
text, paths, events and matchers. JSON preserves the strings and returns arrays
(including empty arrays) in `trusted`, `already`, `lookalikes`, `foreign`,
`outside`, `refused`, `stillTrusted`, `incompatible`, and `warnings`. Unrelated hooks are left alone. A refusal,
lookalike or write failure in the requested scope, or unsafe existing approval
from any source in the listing, makes the command exit non-zero; unrelated-only and empty scopes exit zero.
Later hooks are still classified after a failed write. Exact matches need no
prompt. A missing source path fails closed with an update-Codex hint.

**After upgrading, run `codexrig doctor`.** An old version may already have
approved a lookalike. Doctor probes from the current working directory in the
selected Codex home. This is not a machine-wide audit: approvals in other
repositories are not seen. Any trusted marker-bearing hook in that listing
that is not exact in its own permitted file says **STILL TRUSTED**, including
inline `config.toml` hooks and nested sources. It makes doctor fail and trust
exit non-zero, even outside the requested scope. Refusing a new approval does not
revoke an old one. Stop Codex and follow the reported instructions: remove
`trusted_hash` from the exact `[hooks.state."<reported key>"]` table in the
reported Codex home's `config.toml`, review/remove the handler, restart Codex,
and rerun doctor. Managed approvals require the policy administrator.
There is no automatic revocation, revoke verb, or trust-anyway flag.

Reinstall repairs only recognized current commands and the known legacy Stop
command (`codexrig sync`). It preserves unrelated handlers, including siblings
in the same group. Arbitrary marker-bearing commands and unknown fields require
manual review; doctor offers an install repair only if it would change something.
A global file with only unrelated hooks still warns that backup hooks are missing
and offers installation, but has no trust row. Unrelated hooks beside healthy
codexrig hooks do not alter their health. Disabled hooks are reported as disabled;
if Codex does not list an installed handler, trust is explicitly unknown. Codex
listing warnings are surfaced even alongside successfully listed hooks. An
unknown future listing field fails closed with an update-codexrig hint, without
revocation advice; this differs from an unknown field in the raw hooks file.
Invalid project scopes are refused before classifying any hooks.

Install replaces recognized commands in place when possible. If uninstall or a
repair must move another handler to a new trust-key index, the output names
that handler and the changed keys for review/re-approval. Malformed hooks
document shapes are refused, and large numbers survive rewrites unchanged.

The raw file is snapshotted before the listing and checked unchanged before
writes. Its handler indices, source, command, event and matcher must agree with
the one Codex listing whose key and `CurrentHash` are recorded. codexrig never
fetches a newer hash to approve an older command. This is not an atomic
transaction with Codex: post-check edits and Codex's own hash coverage still
require upstream enforcement. In Codex 0.166.0 on macOS, `commandWindows` did not
change the listing or hash; the raw-field refusal is therefore mandatory.

## Sessions

| Command | What it does |
|---|---|
| `codexrig recent [text]` | What you were working on lately. |
| `codexrig search <text>` | Find a session by what was said in it. |
| `codexrig ledger` | What is remembered, including sessions that have aged out. |
| `codexrig peek list` | Sessions in the repo, whichever machine put them there. |
| `codexrig peek show <id>` | Print one, straight from the git object store. |
| `codexrig peek get <id>` | Copy one onto this machine so `codex resume` can open it. |

`recent` and `search` take `--since`, `--until`, `--cwd`, `--limit`, `--live`,
`--repo` and `--json`. A session found only in the backup says so: `codex resume`
reads your own Codex home, so it has to be restored first — `peek get` is the
quick way to bring one across without restoring anything else.

A session whose rollout has aged out of the retention window is still listed,
marked as remembered rather than resumable, with the `git log` command that
recovers its body from history.

## Configuration

| Key | Meaning |
|---|---|
| `remote` | The private git repo this machine syncs to. |
| `syncSessions` | Carry session rollouts as well as configuration. |
| `redactTranscripts` | Scrub credential-shaped tokens out of staged rollouts. |
| `chunkRollouts` | Store a large rollout as parts, so an append costs a chunk not a copy. |
| `autoRestore` | Restore automatically on a machine with no Codex setup. |
| `alwaysPrune` | Make `restore` prune by default. |
| `hookIntervalMinutes` | How long a hook-driven sync waits before working again. |

```sh
codexrig config get
codexrig config set syncSessions true
codexrig config edit
```

## The rest

| Command | What it does |
|---|---|
| `codexrig repo status` | What the backup holds, by category and size. |
| `codexrig repo gc` | Repack the history. Loses nothing, usually reclaims a lot. |
| `codexrig device list` | The machines syncing into this repo. |
| `codexrig device forget <name>` | Drop a machine's entry. Its files stay. |
| `codexrig guide install` | Write codexrig's blocks into `AGENTS.md`. |
| `codexrig mcp list` | Which MCP servers survive a restore, and what each needs. |
| `codexrig mcp get <name>` | One server in full. `--json` emits the same object `list --json` uses per server — `name`, `transport`, `enabled`, `target` (credentials redacted), `envKeys` (names only, never values), `portability` — and a missing name yields one `{"found":false,…}` object with a non-zero exit. |
| `codexrig ui` | The interactive dashboard, which a bare `codexrig` opens. |
| `codexrig guard` | The PreToolUse hook. Codex calls it; you do not. |
