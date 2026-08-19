# Claude Desktop: session capture and account switching

> Status: **shipped** (2026-08-19) in #198, #199, #200 and #201. Explored against
> live Desktop data on macOS (Claude Desktop with bundled Claude Code 2.1.234).
>
> Kept as a record rather than a plan, because most of what is useful here is the
> places the obvious design was wrong — each noted inline where it applies:
> entropy scanning would have missed the very file that motivated it; sidecar
> mtime is not an activity signal; the CLI and Desktop are routinely signed in as
> different accounts; and tightening an allowlist does nothing to data already
> staged.
>
> **Open follow-up.** Desktop switching is macOS-only. The blocker is the cookie
> swap shelling out to `sqlite3`, which no standard Windows install has;
> `modernc.org/sqlite` is pure Go, needs no cgo (every release build is
> `CGO_ENABLED=0`), and was verified to cross-compile to all six release targets
> and round-trip a BLOB byte-identically, at a cost of roughly +4.6 MB on the
> `clauderig` binary alone. Adopting it would also retire the `.mode insert` SQL
> generation entirely. What it does NOT settle is whether Electron safeStorage
> round-trips under Windows DPAPI the way it does under the macOS Keychain, or
> what the Desktop process looks like for the running-app guard — both need
> checking on the platform before the gate opens.

## What Desktop actually stores

`~/Library/Application Support/Claude` (the `desktop` root in
`config/config.go:110-119`). The parts that matter:

```
config.json                       # settings + ENCRYPTED oauth token caches
claude-code-sessions/<acct>/<org>/local_<id>.json    # 468 KB — sidecar metadata
local-agent-mode-sessions/<acct>/<org>/              #  51 MB — Cowork sandboxes
ant-device-registry.json          # per-account device IDs
```

Both session trees are keyed `<accountUuid>/<orgUuid>/`. On this machine
`03d1c0c9…` is the account (it matches `config.json`'s `lastKnownAccountUuid`)
and `e3055f13…` is the org — **the same org UUID the Claude Code credential
carries**, so the existing account invariant in `account/diagnose.go:29-53`
extends to Desktop unchanged. Both UUIDs are stable per account, so these paths
port across machines as-is. That is the good news, and it is load-bearing for
everything below.

---

## (a) Session capture

Most of this exists. `allowlist/defaults.go:46-59` already syncs both session
trees and `config.json`, `session/session.go` indexes the sidecars for transcript
titles, `mover/plan.go:29` rewrites their `cwd` when a project moves, and
`e2e/desktop_real_test.go` runs against real data. The gaps are three specific
things, two of which are bugs shipping today.

### Bug 1 — the Desktop `config.json` filter keeps a key that doesn't exist

`engine/sync.go:245-250` reduces Desktop's `config.json` to `["preferences"]`.
There is no `preferences` key. The live document's top-level keys are
`updaterLastSeenVersion`, `locale`, `userThemeMode`, `lastKnownAccountUuid`,
`first_launch_at`, the `oauth:tokenCache*` blobs, and per-org `dxt:allowlist*`
entries. So `applyKeepFilter` produces `{}` and we sync an empty object.

The filter's *intent* is right — most of that document is rotating cache and
token material that must never sync. It just names the wrong key. The keys that
are both stable and worth carrying are `locale`, `userThemeMode`, and
`updaterLastSeenVersion`. Fix the list, and keep `preferences` in it so the
filter still works if Desktop reintroduces it.

This is a fail-safe failure — we sync too little, not too much — which is why it
has gone unnoticed. It should still be fixed with a test that asserts against a
real captured `config.json`, because the current test can only have asserted that
filtering an absent key yields nothing.

### Bug 2 — `local-agent-mode-sessions` syncs Cowork sandboxes to the remote

**This one is already live, not hypothetical.** The allowlist takes that tree
wholesale, pruning only `node_modules`. Each `local_<id>/` is a Cowork sandbox
working directory:

```
.audit-key      51 B     <-- secret material
audit.jsonl    7.7 MB    <-- one file, per session
outputs/                 <-- build products
uploads/                 <-- user file uploads, verbatim
.claude/
```

Verified against the live staging repo (`~/.clauderig/repo`, 2.8 GB) and the
remote on 2026-08-18: **463 files tracked under `local-agent-mode-sessions`, and
79 of them are present in `origin/main`'s current tree** — 3 `.audit-key` files,
3 `audit.jsonl`, 59 `outputs/`, 14 `uploads/`. The uploads are user documents
carried byte-for-byte (on this machine: scanned financial registers as PDF, bank
transaction exports, a spreadsheet named for a private individual). The remote is
private — `ghrepo.EnsurePrivate` did its job — so this is not public exposure.
It is still material nobody intended to sync, now in git history, where removing
it needs a history rewrite rather than a delete.

Three distinct failures, worth separating because they need different fixes:

- **Volume.** 51 MB of churning sandbox against a remote that also keeps a
  200-commit `config-history` branch. `outputs/` is regenerable build product —
  exactly the category `vendored()` exists to exclude.
- **Secret material.** `.audit-key` is a credential. It is not JSON, so
  `redact.Scan`'s tripwire (`redact/scan.go:139`) never inspects it. The
  tripwire's blind spot is non-JSON files generally, which is worth fixing
  independently of this allowlist entry.
- **User content.** `uploads/` is arbitrary user-supplied documents. No
  redaction pass can make that safe, because there is nothing structurally
  secret to detect — it is simply not ours to copy. This is the strongest
  argument that the fix belongs in the allowlist, not downstream.

The default-deny posture described at `allowlist/defaults.go:11-19` reads as if
it holds. This is the case where it does not: one `inc()` on a directory whose
contents are unbounded and user-supplied undoes it.

The sidecar `local_<id>.json` files next to these directories are the valuable
part (titles, cwd, model, session graph — 468 KB total). Include those and
exclude the sandbox directories:

```go
inc("local-agent-mode-sessions"),
exc("local-agent-mode-sessions/*/*/local_*/"),   // sandbox working dirs
```

Keep `artifacts.json`, `remote-session-spaces.json` and the small caches at that
level; they are metadata, not sandbox.

### Gap 3 — sidecars outlive the transcripts they point at

Desktop sidecars carry a `cliSessionId` that resolves to a transcript at
`~/.claude/projects/<slug>/<cliSessionId>.jsonl` in the *CLI* root. On this
machine 9 of 15 resolve. The other 6 are dangling: two are marked
`transcriptUnavailable: true` by Desktop itself, and the rest lost their
transcript to retention.

That is the actual defect: `engine/sync.go:109` applies retention by mtime to
the CLI root, and nothing applies it to the sidecars. So the two trees drift
apart monotonically, and `search`/`session` accumulate titles for transcripts
that no longer exist. Retention should be applied to the sidecars on the same
clock — or, better, a sidecar should be retained iff its `cliSessionId`
transcript survives, so the two trees are pruned as one unit rather than two
independent clocks that happen to agree at first.

Note this cuts the other way too: a sidecar is ~1 KB and a transcript can be
megabytes, so keeping sidecars slightly *longer* than transcripts is defensible
if you want titles in `search` history. What is not defensible is the current
state, where the relationship is unspecified and drifts by accident.

---

## (b) Setting Desktop's account

### Where the credential lives, and that we can write it

Desktop keeps OAuth in `config.json` under `oauth:tokenCacheV2` (and a legacy
`oauth:tokenCache`), as an Electron `safeStorage` blob: base64 of `"v10"` +
AES-128-CBC, key `PBKDF2-SHA1(<keychain "Claude Safe Storage">, "saltysalt",
1003, 16)`, IV of 16 spaces. The keychain item reads without prompting, since our
process is not the one being ACL'd against.

**Proven non-destructively:** decrypting both blobs and re-encrypting the
plaintext reproduces the original base64 byte-for-byte. Writing a blob Desktop
will accept is mechanically solved — CBC with a fixed IV and deterministic
padding makes it exact, not approximate.

The decrypted document is keyed by a compound string:

```
<clientId>:<orgUuid>:<audience>:<space-joined scopes>
```

with a `{token, refreshToken, expiresAt, subscriptionType, rateLimitTier}` value.
Two clients are present and Desktop needs both — `9d1c250a…` (`user:inference
user:file_upload user:profile user:sessions:claude_code`) and `a473d7bb…`
(`user:profile` only).

### Why you cannot just transcode the Claude Code credential

The obvious design — take the stored `claudeAiOauth` blob and write it into
Desktop's cache — does not survive contact with the data:

- **The grants are different.** Desktop's tokens and Claude Code's tokens share
  the account and org but are entirely distinct access/refresh pairs, even for
  the shared client `9d1c250a…`. They are issued and refreshed independently.
- **The scope sets differ.** Claude Code holds `user:mcp_servers`, which Desktop
  does not, and the scope string is *part of the cache key* — so there is no key
  under which the CC token is the natural occupant.
- **The second client has no counterpart.** Nothing in the Claude Code
  credential can satisfy the `a473d7bb…` `user:profile` entry.
- **They disagree on subscription.** Desktop reports `max` for this account;
  the Claude Code blob says `pro`. Whatever the reason, it means
  `metaFromBlob`'s `subscriptionType` is not a shared source of truth.

Forging a cache entry for a key whose scopes the token wasn't issued under is
the kind of thing that works in testing and fails on the next server-side scope
check. Don't.

### What to build instead

Treat Desktop's token cache as **its own credential artifact, captured and
restored per account** — symmetric with what `account/` already does for Claude
Code, rather than derived from it.

> Built in [#201](https://github.com/rigsmith/rigsmith/pull/201). One thing the
> design below did not anticipate: the CLI and Desktop are independent logins and
> are routinely signed in as *different* accounts, so capture cannot assume the
> live Desktop session belongs to the account being captured. The implementation
> matches Desktop's `lastKnownAccountUuid` against the account's own
> oauthAccount `accountUuid` and files a session only under its true owner —
> including on switch, where a Desktop signed in as some third account is
> preserved under that account rather than overwritten.

**Store the ciphertext, not the plaintext.** The Safe Storage key is stable for
the life of a machine's keychain, so a blob encrypted on this Mac stays readable
on this Mac. Same-machine switching therefore needs no decryption at all — snapshot
the encrypted values verbatim and write them back. There is no plaintext at rest,
which retires the objection that governed the earlier draft of this section.

```
~/.clauderig/accounts/<id>/desktop-oauth.json     # the v10 blobs, copied verbatim
~/.clauderig/accounts/<id>/desktop-cookies.sqlite # the Cookies DB, copied verbatim
```

### It is two artifacts, not one

Desktop authenticates on two independent surfaces, and a switch has to move both:

| surface | where | used for |
| --- | --- | --- |
| OAuth token cache | `config.json`: `oauth:tokenCache`, `oauth:tokenCacheV2` | API/inference — Claude Code, agent mode |
| Web session | `Cookies` (SQLite): `sessionKey`, `lastActiveOrg`, `__Host-ant_trusted_device` | the claude.ai UI inside Electron |

Both are `v10` Safe Storage ciphertext under the same key, so both copy verbatim
on one machine and neither survives a move to another. Swapping only the token
cache would leave the webview logged in as the previous account.

Alongside them, `lastKnownAccountUuid` in `config.json` selects the active
account. `ant-device-registry.json` is keyed by account UUID and simply
accumulates — it already holds entries for both accounts on this machine and
needs no swapping. The session trees are account-scoped by path and coexist.

### Why a switch costs a login today

Desktop keeps tokens for the **active account only**: `tokenCacheV2` currently
holds entries for one org, with no trace of the second account this machine has
signed into. Logging into B discards A's tokens, so returning to A means signing
in again. Nothing is wrong with A's tokens — they were simply thrown away.

Snapshotting them before switching away is the whole feature.

- `clauderig account add` / `capture` copies the two artifacts into the account
  dir as-is.
- `clauderig account switch` writes the target's artifacts back, sets
  `lastKnownAccountUuid` — and **captures the outgoing account first**, because
  refresh tokens and `sessionKey` rotate, so a snapshot taken once goes stale.
  This is the same staleness problem `CaptureFromSession` already solves for
  Claude Code, and it should reuse that shape.
- Desktop must be fully quit, not merely idle: it holds the Cookies DB open and
  rewrites `config.json` on exit, which would clobber the write. Extend
  `account/livesession.go`'s `RunningInstances` to detect it, and reuse the
  refuse-while-running guard at `commands/account.go:822`.
- Back up the live artifacts before overwriting, matching `BackupLive`
  (`account/account.go:390`), and roll back on failure the way `doSwitch` already
  does at `commands/account.go:905-915`.

### What this does and does not buy

**Does:** unlimited switching between already-authenticated accounts on one
machine, with no re-login.

**Does not:** move an account to a new machine. Both artifacts are sealed with
that machine's Safe Storage key, so the first use of an account on a new Mac is
an ordinary interactive login. That is one login per account per machine, not one
per switch — and it is the honest boundary, because carrying these across
machines would mean decrypting to plaintext and syncing it.

### The constraint that still governs

Even as ciphertext, these are credentials, and they live under
`~/.clauderig/accounts/` — outside both synced roots (the staging repo is
`~/.clauderig/repo`; the roots are `~/.claude` and the Desktop tree). That must
stay true.

Concretely: this must not become a reason to relax `engine.keepOnly` on Desktop's
`config.json`, and `redact.SecretKeys`' `tokencache` entry
(`redact/redact.go:68-79`) must stay. Capturing Desktop credentials is a **local**
operation that makes `switch` work on one machine; it is explicitly not a sync
feature.

### Tested: a verbatim restore works (2026-08-18)

Run against the live app on macOS, with backups taken first:

1. Snapshot `oauth:tokenCache`/`oauth:tokenCacheV2` and the claude.ai cookie rows.
2. Delete both. Launch Desktop → it comes up **signed out**, and sets
   `windowSizeWasSignedIn: false`. It does not self-heal, so the two artifacts
   really are the whole of the local session.
3. Quit. Write both back **verbatim** — ciphertext never decrypted, cookie rows
   re-inserted with `encrypted_value` untouched. Launch Desktop.
4. It comes up **signed in**. `windowSizeWasSignedIn: true`, and both blobs are
   byte-identical to the snapshot afterwards — Desktop accepted them as-is rather
   than re-authenticating.

So same-machine switching by copying these two artifacts is confirmed, with no
decryption, no plaintext at rest, and no re-login. Local Storage, IndexedDB and
the device registry were not touched and were not needed.

Still untested: a *two-account* swap. This validated snapshot → clear → restore
for one account, which exercises the same mechanism, but proving that restoring
A's artifacts over B's live session yields A requires a second login to set up.

### Launch Desktop with a clean environment

Found the hard way while running the above. Desktop was launched with
`open -a Claude` from inside a Claude Code session, so it inherited
`CLAUDE_CONFIG_DIR` — which pointed at a clauderig account profile holding 2
projects instead of the default root's 112. Every session in the Desktop UI then
reported "not found on disk", because Desktop resolved transcripts under the
profile rather than `~/.claude`. Nothing was damaged; relaunching with the
variable unset restored it.

This matters beyond the test: `clauderig account run` sets exactly that variable,
so anything here that launches or relaunches Desktop **must strip `CLAUDE_*` from
the environment first**. A Desktop inheriting a profile's config dir looks
catastrophic — an empty session list — while being purely cosmetic and instantly
reversible, which is its own kind of support hazard.

### A note on what the test did not break

Desktop's re-index during the run flipped `transcriptUnavailable` from 2 sidecars
to 6. Those 4 were already dangling beforehand; Desktop merely caught up with
disk. It is the same drift Gap 3 describes, observed from the app's side.

## Suggested order

Steps 1 and 3 shipped in [#198](https://github.com/rigsmith/rigsmith/pull/198)
(measured on a real Desktop root: 615 included files → 476); step 2 in
[#199](https://github.com/rigsmith/rigsmith/pull/199); step 4 in
[#200](https://github.com/rigsmith/rigsmith/pull/200). Only (b) remains.

A second correction, from building step 4, in the same spirit as the one above.
This document proposed retaining sidecars "on the same clock" as transcripts. Do
not read that as *their own mtime*: a sidecar's mtime records when Desktop last
rewrote its metadata, not when the session was used, and on a real machine
sidecars 32 and 48 days old named transcripts written 0.9 and 2.1 days ago — a
46-day gap. The shipped rule is referential instead: a sidecar survives iff the
transcript it names survives. `local-agent-mode-sessions` is exempt, because a
Cowork transcript lives inside the sandbox that step 1 stopped syncing and so can
never resolve.

One correction from building step 2, worth recording because it inverts the
obvious approach: the tripwire cannot be extended by entropy-scanning file bytes.
The real `.audit-key` measures 3.92–4.03 bits/char — at or below `LooksSecret`'s
own 4.0 threshold — and is binary rather than text, so entropy would have missed
it while false-positiving on transcripts. Since a finding aborts the whole sync,
a false positive there is an outage, not noise. Filenames turned out to be the
high-signal discriminator, in two tiers (key material vs. auth config confirmed
against content) — the second tier forced by four vendored `.npmrc` files in the
plugin marketplace that a name-only rule flagged.

1. **Exclude the Cowork sandbox dirs (Bug 2)** — stops the bleeding. Then decide
   separately whether to rewrite history to remove the 79 files already on the
   remote; that is a judgement call about a private repo, not an emergency, but
   it does not get easier with time.
2. Extend the redaction tripwire to non-JSON files, so the next `.audit-key`-
   shaped thing fails the sync instead of riding it.
3. Fix the `keepOnly` key list (Bug 1) — one line plus a real-data test.
4. Tie sidecar retention to transcript retention (Gap 3).
5. Then (b), the only part that needs new subsystems.

1–4 are independent of each other and of (b). Only 1 is time-sensitive.
