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
| `clauderig account add` | Capture the currently logged-in account into claudeRig's store (and mark it the live one). |
| `clauderig account list` | Show stored accounts; `→` marks the live one. |
| `clauderig account run <id\|email> [-- claude args…]` | **Session mode** — run as that account in *this terminal only*. |
| `clauderig account switch [<id\|email>]` | **Global swap** — change the machine-wide login. Guarded; no arg rotates. `--dry-run` previews; `--force` swaps despite live sessions; `--kill` ends them first. |
| `clauderig account sessions` (alias `ps`) | List running Claude Code instances — what blocks a switch. |
| `clauderig account remove <id\|email>` (alias `rm`) | Stop tracking an account (and delete its session profile). |
| `clauderig account purge` | Remove all of claudeRig's account data. |
| `clauderig account doctor` | Check whether the live credential and `~/.claude.json` name the same account. Exits non-zero on a desync. `--json`, `--journal N`. |
| `clauderig account watch` | Poll for an identity change and record what was running when it happened. `--every` (default 5s). |

`acct` is an alias for `account`. Bare `clauderig account` on a terminal (or
**Accounts** / hotkey `a` from the dashboard) opens an interactive screen — it
shows a ⚠ banner when Claude Code processes are live (`p` lists them), and when
you switch into a blocked state it prompts: **cancel · kill them, then switch ·
force switch**.

## What live testing taught us

Two facts about Claude Code shape the whole design:

1. **Refresh tokens rotate on every refresh.** A captured credential is not a
   stable identity, and a snapshot of an *actively-used* account goes stale fast.
   So accounts are keyed by the **account email** (from `~/.claude.json`), and which one is live is tracked
   by an **explicit pointer** — never inferred from a token. Reference an account
   by any **unique substring** of its email (`relate`, `bri`), its full email, or
   its id. If the same email belongs to two orgs, the second gets a numeric
   suffix (`john-relatecpa-com-2`).
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
clauderig account run you@work.com           # interactive, as that account
clauderig account run you@home.com -- -p "…"  # one-shot; args after -- go to claude
clauderig account run relate                  # any unique substring of the email works (relate, rel, …)
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
- It swaps **both** the credential *and* the account's `oauthAccount` block in
  `~/.claude.json` (email, org, and the plan/`seatTier`/rate-limit tier). The
  plan display lives in that block, separate from the credential — swapping only
  the credential would leave Claude Code showing the previous account's plan
  until a login refresh.
- It **round-trips** the displaced account's current credential *and*
  `oauthAccount` back into its store (keeping those snapshots fresh) and writes a
  timestamped credential backup under `~/.clauderig/cred-backups/`.

When sessions are live you have three ways through:

- `--dry-run` — print the plan and any blockers, change nothing.
- `--kill` — terminate the live processes first (SIGTERM, then SIGKILL for
  stragglers; `TerminateProcess` on Windows), then swap.
- `--force` — swap anyway; the listed sessions keep running but will need to log
  in again on their next refresh.

```sh
clauderig account sessions                       # what's live right now
clauderig account switch --dry-run you@work.com  # preview + guard check, no mutation
clauderig account switch you@work.com            # swap (refuses if Claude is running)
clauderig account switch --kill you@work.com     # end running Claude first, then swap
clauderig account switch --force you@work.com    # swap despite live sessions
clauderig account switch                         # rotate to the next account
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

## Identity desync: Keychain vs `oauthAccount` (diagnosed 2026-08-06)

The signed-in identity lives in TWO places that can disagree, and when they do,
everything user-visible reports the wrong one:

- **Keychain `Claude Code-credentials`** — the actual bearer token. The server
  attributes every request (inference, artifact publishing, usage) to *this*
  account. Ground truth.
- **`~/.claude.json` → `oauthAccount`** — email/org/plan block. Purely local
  belief; the CLI UI and anything that "shows the account" read this.

Observed live: an artifact published while `oauthAccount` said one account landed
on another — `~/.clauderig/accounts/active.json` was the side telling the truth.
This is exactly the point-3b hazard in [CLAUDERIG-DESIGN.md](CLAUDERIG-DESIGN.md):
`switch` must swap the `oauthAccount` block *and* the live credential together.

**clauderig was not the cause** (established by code review 2026-08-07).
`account.WriteLive` has exactly one caller, `doSwitch`, which always writes a
timestamped backup to `~/.clauderig/cred-backups/` *before* touching the Keychain
and aborts if that write fails — on the affected machine that directory did not
exist, so no switch had ever run. The store's account came from a single
`CaptureLive` while both halves still agreed, and `active.json` records what was
live **at capture time**, not a switch. The sync repo carries settings/skills/
plans but never `.claude.json`. The writer that rewrote the block without moving
the credential is still unidentified — which is what `account watch` exists to
catch.

### Diagnosing it

```sh
clauderig account doctor              # both halves side by side; exit 1 on desync
clauderig account doctor --journal 20 # replay recorded identity changes
clauderig account watch               # leave running; records the next flip
```

`doctor` and `watch` append to `~/.clauderig/account-journal.jsonl`, but **only
when the identity-bearing fields change** — the file mtime and the live-process
list are deliberately excluded from the change fingerprint, so a poll loop can't
bury real events under noise. Each recorded flip carries `changed` (which field
moved, and to what) and `live` (every Claude Code process alive at that instant,
with its cwd), which is what makes attribution possible after the fact. Both are
read-only: neither ever writes a credential.

`add` and `list` run the same check and warn, since `list`'s `→` shows only
claudeRig's *pointer* — never proof of what the server sees.

Two hardening changes went in alongside:

- `switch` now reads the target's stored `oauthAccount` block **before** any
  mutation and refuses if it's missing. Previously that write was best-effort and
  happened *after* the credential had already moved, so a target with no stored
  block produced exactly this desync and still reported `Switched to …`.
- The package's test fixtures generated the credential org and the block org from
  unrelated strings, so the suite implicitly asserted they need not match — which
  is why this class of bug was invisible to it. `diagnose_test.go` encodes the
  real invariant: for healthy state, `credential.organizationUuid` ==
  `oauthAccount.organizationUuid`.

**Detecting a desync without reading the Keychain** (often permission-blocked):
`~/.claude.json` `groveConfigCache` is keyed by account UUID and holds
server-returned payloads — the entry with real content (e.g. `grove_enabled`)
belongs to the TRUE account; a timestamp-only shell is local-only belief.
`passesEligibilityCache` referral codes also distinguish identities (same code
across two org UUIDs = one person in two orgs). Note both accounts appear only as
bare UUIDs — a text grep for the email misses this.

**Recovery:** resync both halves — `clauderig account switch <name>` or
`claude /login` — and never from inside a live session. The planned UI's account
face runs this check as a health probe (see
[CLAUDERIG-UI-PLAN.md](CLAUDERIG-UI-PLAN.md), Phase 3).
