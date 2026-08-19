# claude-swap review — what to take, what we already have, what to leave

> Reviewed 2026-08-19 against [claude-swap](https://github.com/realiti4/claude-swap)
> by realiti4 (MIT) at `1c3122e`, plus its open and recently-merged PRs.
> `clauderig account` is a clean-room Go reimplementation of that project's
> concept, and it stays worth re-reading: it is ~24k lines of Python now, and
> most of what it has learned since we forked the idea was learned the hard way,
> on real machines, from real logouts.

## Adopted (implemented in this PR)

### 1. Cooperate with Claude Code's own credential locks — *claude-swap #167*

The one real race between a switch and a running Claude Code. Its token refresh
**reads the credential, does a network round trip, and saves** — all under two
advisory locks. A swap landing inside that window is overwritten by the refreshed
*old* account's token, and the backup taken a moment earlier preserves a refresh
token that has already been rotated away. Held under the lock, Claude Code's own
double-checked re-read sees the swapped (non-expired) credential and abandons the
refresh instead.

We re-verified the protocol against the bundle installed on this machine
(2.1.227, its `FIa`/`ePa` helpers) rather than porting claude-swap's constants on
trust:

| | |
|---|---|
| Mechanism | npm `proper-lockfile` — the artifact is a **directory**, `mkdir` is the mutex |
| Primary lock | `<config-home>/.oauth_refresh.lock` |
| Legacy lock | `<realpath(config-home)>.lock` → `~/.claude.lock`, kept for external tools |
| Timings | `stale: 60000, update: 5000` — stale only past **60s**, holders touch every 5s |
| Contention | On a held legacy lock, Claude Code **releases the primary** and retries, 5× with 1–2s jitter |

Two details are load-bearing and easy to get wrong: never steal a lock younger
than 60s (a live holder's toucher can stall much longer than its 5s interval —
suspend, blocked event loop — while still legitimately owning it), and take the
pair in Claude Code's order, releasing the primary if the legacy lock is
contended. Mirroring both is what keeps a waiting clauderig and a waiting Claude
Code from deadlocking against each other.

Implemented in `internal/clauderig/account/claudelock.go`; `doSwitch` holds the
pair across the whole read-modify-write. It costs an uncontended `mkdir` when
nothing else is running, and it matters most in exactly the case our
running-instance guard permits: `--force`, or a session the process scan missed.

### 2. `add` must not capture from inside a session profile — *claude-swap #190, #205*

`add` reads the **machine-wide** login on purpose. Run from a `clauderig account
run` terminal, that is a *different account* than the one the operator is looking
at, so the capture files the default profile's credential under whatever
`~/.claude.json` happens to name — a mislabeled pair, which the switch guard then
correctly refuses to use. It now refuses at capture time and names the two paths
that do what was meant.

Claude Code resolves its credential store through `CLAUDE_SECURESTORAGE_CONFIG_DIR`
first and `CLAUDE_CONFIG_DIR` second (both present in 2.1.227), so both are checked,
in that order.

### 3. Write through a symlink, never over it — *claude-swap #201*

`~/.claude.json` is routinely symlinked into a dotfiles repo. Our atomic write
renamed onto the link path, which replaces the link with a regular file: nothing
errors, both copies keep working, and they diverge from that moment on.
`atomicWriteFile` now resolves the destination first — which also keeps the
rename atomic, since the temp file lands in the real target's own directory.

## Already covered — do not re-adopt

Worth recording, so the next reader of claude-swap's issue list doesn't re-derive
these:

| claude-swap | clauderig equivalent |
|---|---|
| #237 — pairing one profile's identity with another's credential | `sessionKeychainService()` derives the per-profile Keychain service from SHA-256 of the config dir; verified live 2026-08-18 |
| #216 — refuse a credential whose owner is not the account being added | `doSwitch` refuses a stored pair whose credential org and profile org disagree; `add` warns rather than refuses, deliberately, to keep capture-then-repair open |
| Dead/blanked credential handling | `HasTokens` guards both directions — `SaveCredential` refuses to store one, `doSwitch` refuses to switch into one |
| Degraded Keychain reads (#196's first half) | `ReadLive` propagates a Keychain *error* instead of falling back; only a genuine "no such item" reads the file, so a locked keychain can't be mistaken for an empty one |
| Process detection | `RunningInstances` reads both session registries **and** the process table, excluding isolated `account run` profiles by their `CLAUDE_CONFIG_DIR` |
| Round-trip backup before overwrite | `BackupLive` + `~/.clauderig/cred-backups`, and the switch aborts if the backup can't be written |

Most of #196 (never consume a stale refresh token; CAS on refresh-token
fingerprint; quarantine hygiene) does **not** apply to us: clauderig never POSTs
to the token endpoint. It moves credential blobs and lets Claude Code do its own
refreshing. That is a deliberate scope difference, and it removes an entire class
of one-time-use-grant bugs that claude-swap has had to engineer around.

## Proposed — not built, ranked

1. **`--json` on `list` / `switch` / `status`.** We have it on `doctor` only.
   Cheapest real win; makes the whole command scriptable. claude-swap emits one
   object on stdout with human notices on stderr, which is the right shape.
2. **`disable` / `enable`.** Hold an account out of rotation without untracking
   it — bare `clauderig account switch` rotates to the next account, and there is
   currently no way to say "not that one". Small state addition, immediately useful.
3. **Aliases.** `switch dev` instead of an email. Pure ergonomics, low risk.
4. **Directory → account mapping.** Bind a repo to an account so a bare
   `account run` in it launches the right one. Fits clauderig's worktree posture
   well; the mapping is per-machine and must not sync.
5. **Usage-aware switching (`auto`).** claude-swap's largest subsystem: 5h/7d and
   per-model windows, strategies (`best`, `next-available`, `consume-first`),
   cooldown plus hysteresis to stop flip-flopping, adaptive polling to stay inside
   rate limits, quarantine of dead accounts, exit codes for cron. Genuinely
   useful and genuinely large — it needs a usage API client, a usage store, and a
   polling policy before any of the switching logic. Worth a design doc of its
   own before any code, and worth asking whether it belongs in clauderig at all
   or in a sibling tool.
6. **Export / import for machine migration.** Note our deliberate constraint:
   clauderig syncs config but **never** syncs accounts, so an explicit,
   user-driven export is the only sanctioned way to move them. If we build it,
   take claude-swap #195 with it (close the world-readable window on the exported
   file).
7. **Windows: retry an atomic replace past transient sharing failures**
   (claude-swap #158). Latent for us until someone switches accounts on Windows
   with Claude Code running.

## Not adopting

- **Desktop account switching.** claude-swap has an open PR for it too (#230).
  We built it, shipped it to `main`, and withdrew it the same day — see
  [CLAUDERIG-ACCOUNTS.md](CLAUDERIG-ACCOUNTS.md#why-not-claude-desktop) for the
  reasoning. Nothing in their approach changes the underlying obstacles.
- **Multi-provider (Codex/ChatGPT) support** (#252). clauderig is a Claude Code
  tool; this would double the surface for a different product's auth model.
- **Menu bar app** (#258), **Claude in Chrome sync** (#242), **TUI usage
  dashboard**. Out of scope for a CLI that already has a `--fix`-style doctor and
  a small interactive screen.

## Credit

`clauderig account` already credits claude-swap in its package comment and in
[CLAUDERIG-ACCOUNTS.md](CLAUDERIG-ACCOUNTS.md). Everything adopted above is
credited inline at the code that implements it, by PR number, so the provenance
survives contact with a future reader who has never heard of the project.
