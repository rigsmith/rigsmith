# codexRig commands

## The lifecycle

| Command | What it does |
|---|---|
| `codexrig init` | Write the config, pick a private remote, install the hooks. |
| `codexrig sync` | Capture this machine's Codex setup and push it. |
| `codexrig pull` | Bring down what another machine synced. |
| `codexrig restore` | Write the synced setup onto this machine. |
| `codexrig status` | Where this machine stands. `--json` for scripts. |
| `codexrig doctor` | Check it is actually working. `--fix` repairs what it can. |

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
| `codexrig account disable <ref>` | Hold it out of automatic selection. |
| `codexrig account doctor` | Check the live login and codexrig's record agree. |
| `codexrig account sessions` | Which Codex processes are running. |

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
| `codexrig global trust` | Record them as trusted, so Codex runs them. |
| `codexrig global status` | Installed? Up to date? Trusted? |
| `codexrig project install` | The branch guard, in this repository's `.codex/`. |
| `codexrig project trust` | Same, for the repository's hooks. |

Install and trust are separate because they are different acts: one writes a
file, the other tells Codex to trust a file. `trust` asks Codex for the hash
rather than computing one, and makes the edit through Codex so `config.toml`
keeps its own formatting.

## Sessions

| Command | What it does |
|---|---|
| `codexrig recent [text]` | What you were working on lately. |
| `codexrig search <text>` | Find a session by what was said in it. |

Both take `--since`, `--until`, `--cwd`, `--limit`, `--live`, `--repo` and
`--json`. A session found only in the backup says so: `codex resume` reads your
own Codex home, so it has to be restored first.

## Configuration

| Key | Meaning |
|---|---|
| `remote` | The private git repo this machine syncs to. |
| `syncSessions` | Carry session rollouts as well as configuration. |
| `redactTranscripts` | Scrub credential-shaped tokens out of staged rollouts. |
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
| `codexrig guide install` | Write codexrig's blocks into `AGENTS.md`. |
| `codexrig mcp list` | Which MCP servers survive a restore, and what each needs. |
| `codexrig ui` | The interactive dashboard, which a bare `codexrig` opens. |
| `codexrig guard` | The PreToolUse hook. Codex calls it; you do not. |
