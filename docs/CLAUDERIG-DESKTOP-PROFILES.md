# clauderig desktop — several Claude Desktop accounts, side by side

> Status: **implemented** (2026-08-19). Replaces the withdrawn Desktop
> *session-switching* feature with the opposite model.

Claude Desktop holds one login at a time. `clauderig desktop` gives each account
its own permanent Electron profile, so every account stays logged in and their
windows can be open together.

```console
$ clauderig desktop add work        # creates a profile, opens a window to log into
$ clauderig desktop add personal    # a second window; the first stays logged in
$ clauderig desktop open work       # from now on, just open the one you want
```

> **Credit.** The model — and the macOS launch mechanism — come from
> [**guise** by siddhjagani](https://github.com/siddhjagani/guise) (Rust, macOS
> only). This is an independent Go implementation that adds Windows support and
> wires the profiles into clauderig.

## Why this model, when the last one was withdrawn

clauderig shipped Desktop *session switching* — snapshot the signed-in session,
restore it on switch — and withdrew it the same day. The obstacles were
properties of the app, not gaps in the implementation (see
[CLAUDERIG-ACCOUNTS.md](CLAUDERIG-ACCOUNTS.md#why-not-claude-desktop)): Desktop
signs in twice at moments a capture cannot see, Electron rewrites its config and
holds its cookie database open so writes underneath a running app are silently
lost, and reading the session at all means driving a private Chromium sqlite
schema.

**This model moves no sessions.** Each account gets its own `--user-data-dir` and
Claude Desktop is launched against it. Every obstacle above simply stops
applying:

| Session switching (withdrawn) | Profiles (this) |
|---|---|
| Captures a session mid-sign-in and can't tell | Never captures anything |
| Electron clobbers writes under a running app | Never writes into a profile |
| Drives a private sqlite cookie schema | Never opens the cookie database |
| Must refuse while Desktop is running | Several instances run at once, on purpose |
| One account logged in at a time | All of them, permanently |

The trade is real and worth stating: profiles do **not** share state. Each has
its own settings, MCP servers, and chat history, because each is a separate
Claude Desktop installation as far as the app is concerned. Session switching
promised one identity moving between windows; this promises many identities in
many windows.

## Commands

| Command | What it does |
| --- | --- |
| `clauderig desktop add <name> [--email X]` | Create a profile, seed it from your existing install, and open a window to log into. `--no-seed` starts empty. |
| `clauderig desktop open <name\|email>` | Open the profile's window, or focus it if already open. |
| `clauderig desktop list` (alias `ls`) | Saved profiles; `●` marks the ones open right now. |
| `clauderig desktop quit <name\|email>` | Close that profile's window (SIGTERM, then firmly). |
| `clauderig desktop rm <name\|email> [--force]` | Delete the profile. Signs that account out of Desktop for good. |
| `clauderig desktop map [<name>] [dir]` / `unmap [dir]` | Bind a directory to a profile, so a bare `desktop open` there opens it. Bare `map` lists every binding. |
| `clauderig desktop share [<name>]` / `unshare [<name>]` | Share Claude Code session history between profiles — and bring it into `clauderig sync`. `--all`, `--cowork`. |

`clauderig desktop list --json` emits one machine-readable object — the profiles,
which are open, and whether Desktop is supported and installed on this platform.

Bare `clauderig desktop` on a terminal opens an interactive screen: the profiles,
`●` against the open ones, and keys to open, close, add and remove. It is also
reachable from the dashboard (`clauderig ui`, hotkey `d`). Note what the screen
does *not* have: a "current" profile. There isn't one — every profile is a
separate login and any number can be signed in at once, so `●` means "window
open", not "this is the active account".

## Directory mapping

`clauderig desktop map work ~/clients/acme` binds a directory; a bare
`clauderig desktop open` inside it opens that window. Subdirectories inherit the
nearest mapped ancestor.

This is the **same table** `clauderig account map` writes
(`~/.clauderig/dir-map.json`), so one directory can name both the CLI account and
the Desktop profile it belongs to — they usually travel together, and one file
means either command's `map` shows the whole picture rather than half of it. The
two bindings stay independent: `desktop unmap` never drops the account binding,
and vice versa. Mappings are per-machine and never synced.

`app` is an alias for `desktop`. The group is deliberately **separate from
`clauderig account`**, which switches the Claude Code CLI login: two different
products, two independent logins, and conflating them is what produced the
original bug report.

## How it works

Each profile is a directory under `~/.clauderig/desktop/<name>/`:

```
~/.clauderig/desktop/work/
  profile.json     # clauderig's metadata: name, email label, linked account id
  data/            # --user-data-dir — Claude Desktop owns every byte in here
```

**Launching.** On macOS, `open -n -a Claude.app --args --user-data-dir=<dir>`.
`open -n` (rather than exec'ing the binary) hands the process to LaunchServices,
so it detaches from the terminal — a direct launch stays in the caller's session,
steals focus, and dies with the shell. On Windows, `claude.exe --user-data-dir=<dir>`
with `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`, which is the same idea.

**Identifying an instance.** Every operation matches the full
`--user-data-dir=<dir>` token in the process command line — `pgrep -f` on macOS,
a `Get-CimInstance Win32_Process` JSON query on Windows. Matching the whole token
is what stops `quit work` from also killing `work-2`, whose path shares a prefix.

**Platforms.** macOS and Windows, because that is where Anthropic ships Claude
Desktop. Elsewhere the command reports the platform as unsupported and points at
`clauderig account`, which works everywhere. Nothing guesses at a community
Linux build's layout.

## Safety properties

- **No credential is ever read.** clauderig creates an empty directory and
  launches an app at it. Compare the withdrawn feature, which exported cookies
  and token caches.
- **Profiles never sync.** They live under `~/.clauderig`, outside both sync
  roots (`$HOME/.claude` and the Desktop application-support directory). A
  regression test asserts this, because a profile inside a sync root would push
  live sessions to the remote.
- **Profile directories are 0700 and `profile.json` is 0600** — on macOS and
  Linux. Windows
  has no Unix permission bits (Go's `Chmod` there only toggles read-only), so
  containment on Windows rests on the ACL inherited from `%USERPROFILE%`.
- **`rm` refuses while the window is open** unless forced, then closes it first:
  deleting a live Electron profile leaves the app writing into unlinked files.
- **Names are validated** before they become directory names, so a name cannot
  walk out of the store root.

## Integration with `clauderig account`

The link is by name, not by credential. `desktop add work` looks `work` (and
`--email`) up in the account store and records the matching account id, so
`desktop list` can show `↔ john-brightshore-io`. That is a **label**: the CLI
login and the Desktop login remain entirely independent, which is the truth of
how the two products work and the thing the withdrawn feature obscured.

## What a new profile inherits

A fresh `--user-data-dir` is genuinely empty, which is right for the *login* and
needlessly unhelpful for everything else — your MCP servers and theme are yours,
not the account's. So `desktop add` seeds a new profile from the existing Claude
Desktop install:

| Copied | Not copied |
|---|---|
| `claude_desktop_config.json` (MCP servers) | `Cookies`, `Local Storage`, `Session Storage` — the claude.ai session |
| `config.json` → `locale`, `userThemeMode`, `preferences` | `config.json` → `oauth:tokenCache`, `oauth:tokenCacheV2`, `lastKnownAccountUuid` |
| `extensions-blocklist.json`, `git-worktrees.json`, `cowork-enabled-cli-ops.json` | `dxt:*` caches, `updaterLastSeenVersion`, `first_launch_at` |

`config.json` is **rebuilt** from the allowed keys rather than copied and
filtered, so the safe set is additive: a key nobody has vetted is absent by
default. That list is `config.DesktopConfigKeepKeys()` — the same one
`clauderig sync` uses to prune the synced copy, so seeding and sync can never
disagree about what is safe to copy, and a test asserts the credential-bearing
keys are not in it.

A seeded profile still starts **signed out**. That is the point: settings are
portable, logins are not, and copying a login between profiles is exactly what
the withdrawn session-switching feature did. `--no-seed` starts from nothing.

Seeding happens before the window launches, because Desktop writes its own
`config.json` on first run and seeding underneath a started app would race it.

## Sharing session history

By default each profile has its own chat history, because each is a separate
installation as far as the app is concerned — a Claude Code session started in
the `work` window does not appear in `personal`, and clauderig's sync only
watches the *default* Desktop root, so profile history is not backed up at all.

`clauderig desktop share` fixes both at once:

```console
$ clauderig desktop share work        # or --all for every profile
✓ sharing work · john@work.com
    claude-code-sessions → shared (14 migrated, 0 already there)
shared tree: ~/Library/Application Support/Claude
`clauderig sync` already covers it, so this history is backed up now too
```

It points the profile's `claude-code-sessions` at the **default Desktop root's**
own directory. That location is the point: the Desktop allowlist already includes
`claude-code-sessions`, so sharing brings profile history into the existing
backup with no new sync rules, no new root, and no new retention story.

The root is resolved from your **saved** configuration, not the compiled-in
defaults — `clauderig init` can persist the Desktop root disabled, and sync skips
disabled roots. When that is the case `share` still links the profiles (the
sharing itself is useful) but says plainly that the history is *not* backed up,
rather than promising a backup that will never run.

**Why this is safe.** Both session trees are already partitioned by account uuid
— `claude-code-sessions/456fc32e-…/`, `claude-code-sessions/03d1c0c9-…/` — so two
profiles signed into different accounts write to different subdirectories and
cannot collide. Two profiles signed into the *same* account share one
subdirectory, which is the intent rather than a bug.

**Nothing is destroyed, and a failure changes nothing.** The existing directory
is moved aside rather than deleted, and put back if the link cannot be created —
a Windows junction refused for want of privilege being the realistic case.
Deleting first would leave the profile with *no* session directory, and Claude
Desktop would quietly build a fresh empty tree in its place. Retrying after the
cause is fixed is a no-op rather than a duplication, because migration never
overwrites what the first attempt already copied.

Existing history is copied into the shared tree before the directory moves, and
migration **never overwrites**: a file already
present is left exactly as it is and counted as skipped (enforced with `O_EXCL`,
not just a pre-check, so a concurrent writer cannot slip in between). The shared
tree may hold the default profile's own history, and clobbering that would
destroy the very sessions this feature exists to preserve.

**The window must be closed.** Electron keeps writing through a directory handle
it opened before the swap, so relinking a live profile would silently lose
whatever it writes next. `share` refuses while the profile is open — and refuses
on an *unknown* state too, rather than moving history on a guess.

`clauderig desktop unshare` puts a profile back on its own directory. It is
deliberately non-destructive: the shared history stays where it is. Working out
which sessions "belong" to a profile and copying them back would be guesswork,
and getting it wrong would either duplicate or delete history — stopping the
sharing is reversible, deleting is not.

`--cowork` extends both commands to `local-agent-mode-sessions`. It is opt-in
because that tree is two orders of magnitude larger (51 MB against 768 KB on the
machine this was built on) and holds Cowork sandbox working directories, so
moving it is a heavier operation than most callers want by default. Note that
sync already excludes the sandbox contents (`local-agent-mode-sessions/*/*/local_*`)
while keeping the session metadata beside them, so sharing it does not widen what
leaves the machine.

**Platforms.** A directory symlink on macOS and Linux. On Windows a **junction**:
creating a directory symlink there needs `SeCreateSymbolicLinkPrivilege`, which a
normal user does not have unless Developer Mode is on, while `mklink /J` needs no
privilege at all. `os.Symlink` is still tried first, since it is the more
standard artifact when the privilege is available.

`desktop list` marks a sharing profile with `shared history`, `--json` reports it
as `sharedHistory` alongside the `sharedRoot`, and the interactive screen toggles
it with `s` (offered only for a profile known to be closed).

## Verification status

The profile store, name validation, instance bookkeeping and the sync-root
exclusion are covered by tests. The **real** launch paths are not: they shell out
to `open`/`powershell` and would open live windows, so they are exercised through
a fake.

That gap has already cost one bug. The macOS `pgrep` invocation was missing an
end-of-options `--`, so it rejected every pattern (which begins with `--`) and
returned an error for every profile — and a swallowed error rendered that as
"closed". `list` would have shown everything closed, `open` would have launched
duplicate windows, and `rm` would have deleted a live profile. A fake cannot see
that. There is now a gated real-machine test for the scan specifically:

```console
CLAUDERIG_REAL_DESKTOP=1 go test ./internal/clauderig/desktop/ -run RealScan
```

Before this is relied on, still run once by hand on each platform:

```console
clauderig desktop add scratch     # a window opens; log in
clauderig desktop list            # shows ● scratch open
clauderig desktop open scratch    # focuses, does not open a second window
clauderig desktop quit scratch    # window closes
clauderig desktop rm scratch      # profile gone
```

Windows in particular is written from the documented install layout
(`%LOCALAPPDATA%\AnthropicClaude`, stub launcher plus `app-<version>` directories)
and has not been run on a Windows machine yet.
