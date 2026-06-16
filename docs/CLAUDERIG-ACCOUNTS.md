# clauderig accounts — multiple Claude Code logins, one machine

Run several Claude Code accounts (work, personal, a client's) from one machine —
either as fully isolated, self-refreshing per-terminal sessions, or by swapping
the machine-wide login.

> **Credit.** The concept and the safety mechanisms — process detection,
> `security -i` writes, round-trip backup — come from
> [**claude-swap** by realiti4](https://github.com/realiti4/claude-swap) (MIT).
> `clauderig account` is a clean-room Go reimplementation inside clauderig.

## Commands

| Command | What it does |
| --- | --- |
| `clauderig account add [--label work]` | Capture the currently logged-in account into claudeRig's store (and mark it the live one). |
| `clauderig account list` | Show stored accounts; `→` marks the live one. |
| `clauderig account run <id\|label> [-- claude args…]` | **Session mode** — run as that account in *this terminal only*. |
| `clauderig account switch [<id\|label>]` | **Global swap** — change the machine-wide login. Guarded; no arg rotates. `--dry-run` previews; `--force` swaps despite live sessions; `--kill` ends them first. |
| `clauderig account sessions` (alias `ps`) | List running Claude Code instances — what blocks a switch. |
| `clauderig account remove <id\|label>` (alias `rm`) | Stop tracking an account (and delete its session profile). |
| `clauderig account purge` | Remove all of claudeRig's account data. |

`acct` is an alias for `account`. Bare `clauderig account` on a terminal (or
**Accounts** / hotkey `a` from the dashboard) opens an interactive screen — it
shows a ⚠ banner when Claude Code processes are live (`p` lists them), and when
you switch into a blocked state it prompts: **cancel · kill them, then switch ·
force switch**.

## What live testing taught us

Two facts about Claude Code shape the whole design:

1. **Refresh tokens rotate on every refresh.** A captured credential is not a
   stable identity, and a snapshot of an *actively-used* account goes stale fast.
   So accounts are keyed by a **stable label**, and which one is live is tracked
   by an **explicit pointer** — never inferred from a token.
2. **Mutating the live credential under a running Claude Code instance forces a
   re-login.** So `switch` is **guarded** by live-session detection, and session
   mode never touches the live credential at all.

## Session mode (`run`) — the safe, primary path

Each account gets a **persistent, isolated `CLAUDE_CONFIG_DIR`** at
`~/.clauderig/accounts/<id>/config`. `run` execs `claude` against it, so this
terminal is that account while every other terminal and the VS Code extension
stay on your default.

- The profile **self-refreshes its own token in isolation** and **never touches
  your live login** — it can't disturb a working session.
- The credential is seeded from the store only when the profile is new or marked
  stale (e.g. after you re-`add` that account); a session's own refreshed token
  is otherwise left intact.
- `~/.claude` customizations (`settings.json`, `CLAUDE.md`, `skills`, `commands`,
  `agents`, `plugins`, …) are shared in by default (symlink, copy fallback);
  credentials and history stay isolated. `--no-share` gives a bare profile.

```sh
clauderig account run work                 # interactive, as "work"
clauderig account run personal -- -p "…"   # one-shot; args after -- go to claude
```

## Global swap (`switch`) — machine-wide, guarded

`switch` overwrites the live credential the whole machine reads, so every Claude
Code instance follows. Because that logs out anything currently running, it is
**guarded**:

- It **refuses** (non-zero exit, listing the offending processes) if any Claude
  Code instance is live — detected from `~/.claude/sessions/{pid}.json` and
  `~/.claude/ide/{port}.lock` (verify with `clauderig account sessions`). The
  detection catches more than Claude Code windows — e.g. desktop apps that embed
  the Claude agent SDK also hold the credential.
- It **round-trips** the displaced account's current credential back into its
  store (keeping that snapshot fresh) and writes a timestamped backup under
  `~/.clauderig/cred-backups/`.

When sessions are live you have three ways through:

- `--dry-run` — print the plan and any blockers, change nothing.
- `--kill` — terminate the live processes first (SIGTERM, then SIGKILL for
  stragglers; `TerminateProcess` on Windows), then swap.
- `--force` — swap anyway; the listed sessions keep running but will need to log
  in again on their next refresh.

```sh
clauderig account sessions                # what's live right now
clauderig account switch --dry-run work   # preview + guard check, no mutation
clauderig account switch work             # swap (refuses if Claude is running)
clauderig account switch --kill work      # end running Claude first, then swap
clauderig account switch --force work     # swap despite live sessions
clauderig account switch                  # rotate to the next account
```

Prefer `run` for parallel accounts; reach for `switch` only when you genuinely
want the machine-wide default login to change.

## Storage & platform notes

- Accounts live under `~/.clauderig/accounts/<id>/` — `meta.json`,
  `credential.json` (`0600`), and the persistent `config/` profile. The live
  pointer is `accounts/active.json`. Credentials are never printed or logged.
- **Live store** (what `switch` writes): the macOS **Keychain**
  (`Claude Code-credentials`) on a Mac, or `~/.claude/.credentials.json` on
  Linux/WSL/Windows. On macOS the Keychain takes precedence over the file, so the
  swap goes through the Keychain — written via `security -i` with the secret
  passed as **hex over stdin**, so it never appears in process argv (only an
  oversized payload falls back to argv, still as hex). `/usr/bin/security` is
  pinned against PATH hijacking.
- **Windows** — symlink-based sharing needs Developer Mode or an elevated shell;
  otherwise claudeRig copies the customizations into the session instead.
