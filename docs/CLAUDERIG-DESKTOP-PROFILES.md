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

## Setting it up

### 1. One profile per account

```console
$ clauderig desktop add work        # creates the profile, opens a window
                                    # → log in as work in that window
$ clauderig desktop add personal    # a second window; work stays logged in
                                    # → log in as personal
```

That is the whole setup. You log in **once per profile** — from then on
`clauderig desktop open work` reopens it already signed in, and both windows can
be open at the same time.

`add` seeds each new profile from your existing Claude Desktop install, so your
MCP servers, theme and locale are already there
([what it copies](#what-a-new-profile-inherits)). The login is the one thing it
does not copy, and deliberately so: Desktop's OAuth refresh token is
single-use, so two profiles holding one copy of it would work until the first
refresh and then sign one of them out at an unpredictable moment. Logging in
gives each profile a credential it owns.

### 2. There is no step 2

`clauderig sync` picks profiles up on its own — settings and chat history,
never the login. Nothing to enable, nothing to run with the windows closed.
[How that works](#backup-and-restore).

```console
$ clauderig desktop list
Claude Desktop profiles
● work · john@work.com      open    ↔ john-work-com
  personal · john@home.com  closed
each profile is its own login — opening one never signs another out
chat history is per profile — `clauderig sync` backs each one up separately
```

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

Backed up separately, too — [see below](#backup-and-restore). Not pooled: the
app partitions history by account, and clauderig honours that boundary rather
than working around it.

## Commands

| Command | What it does |
| --- | --- |
| `clauderig desktop add <name> [--email X]` | Create a profile, seed it from your existing install, and open a window to log into. `--no-seed` starts empty. |
| `clauderig desktop open [<name\|email>]` | Open the profile's window, or focus it if already open. |
| `clauderig desktop list` (alias `ls`) | Saved profiles; `●` marks the ones open right now. |
| `clauderig desktop quit [<name\|email>]` | Close that profile's window (SIGTERM, then firmly, then confirmed). |
| `clauderig desktop rm <name\|email> [--force]` | Delete the profile. Signs that account out of Desktop for good. |
| `clauderig desktop map [<name>] [dir]` / `unmap [dir]` | Bind a directory to a profile, so a bare `desktop open` there opens it. Bare `map` lists every binding. |
| `clauderig desktop shortcut [<name\|email>] [--to desktop\|apps]` | Make a clickable launcher for the profile. `--all` for every profile, `--rm` to delete them. |

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

### Which profile a command acts on

`open` and `quit` both take an optional name and resolve it the same way:

1. **the profile you named**;
2. **the one bound to this directory** (`desktop map`, nearest mapped ancestor);
3. on a terminal, **one you pick** from a list — `quit` offers only the windows
   that are actually open;
4. off a terminal, an **error** naming both ways to say which.

Neither command ever picks for you. With several profiles, silently choosing one
is the surprising outcome — and erroring at someone sitting at a prompt is the
unhelpful one, which is why step 3 exists.

`app` is an alias for `desktop`. The group is deliberately **separate from
`clauderig account`**, which switches the Claude Code CLI login: two different
products, two independent logins, and conflating them is what produced the
original bug report.

## Shortcuts

`clauderig desktop shortcut work` writes a clickable launcher for one profile:

- **macOS** — a small `.app` bundle (`Claude - work.app`), carrying Claude's own
  icon. It is built locally, so it is not quarantined and Gatekeeper does not
  gate it; no signing identity is involved.
- **Windows** — a real `.lnk`, created through the `WScript.Shell` COM object,
  with its icon taken from `claude.exe`.

`--to desktop` (the default) writes to the desktop — asked of Windows rather
than composed from `%USERPROFILE%`, so a OneDrive-redirected desktop is still
the one you see. `--to apps` writes to `~/Applications` on macOS (which puts it
in Spotlight and Launchpad) and the Start Menu on Windows. Repeat `--to` for
both, `--label` renames it, `--all` does every saved profile.

**The shortcut runs `clauderig desktop open <name>`, not Claude directly.** That
costs a process per click and buys the things the `--user-data-dir` flag alone
cannot do: a second click focuses the open window instead of starting a second
instance on the same profile, `lastOpened` stays true, and a profile that has
never been launched still gets seeded. A shortcut is a click-sized `desktop
open`, not a second launch path to keep in step with the first.

clauderig is named by **absolute path** inside the shortcut, because a GUI launch
inherits none of the shell's `PATH`. That is the one thing that can rot: move or
reinstall the binary and the shortcut points at nothing. Both platforms fail
loudly rather than silently — macOS shows an alert naming the missing path,
Windows shows the shell's own "target unavailable" dialog — and re-running
`clauderig desktop shortcut <name>` rewrites them. The path is taken unresolved,
so a Homebrew install stays pinned to `/opt/homebrew/bin/clauderig` rather than
to the versioned Cellar path behind it, which would break on the next upgrade.

A shortcut is recognised again by a **marker inside the artifact** — a file in
the bundle's `Resources/` on macOS, the shortcut's description on Windows —
never by a manifest kept beside it. A manifest goes stale the moment an icon is
renamed or moved, and a stale manifest is what turns "remove this profile's
shortcuts" into "delete a file that is no longer ours". So `--rm` and
`clauderig desktop rm` only ever delete what carries the marker, and a file of
the same name that clauderig did not write is refused until `--force`.

`clauderig desktop add` offers a desktop shortcut at the end on a terminal;
`--shortcut` makes one without asking and `--no-shortcut` skips the question.

What a shortcut **cannot** do is brand the window it opens. macOS and Windows
label a window by the application that owns it, and that application is Claude
Desktop for every profile — so the icon and name are Claude's once the window is
up, however the shortcut is labelled. Short of copying the whole app bundle per
profile (which breaks updates and the signature), there is no way around it.

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
- **No credential ever syncs.** Sync reaches into a profile deliberately
  ([backup](#backup-and-restore)), but only through the include-only Desktop
  allowlist — the same named files it takes from the machine-wide install. The
  store itself still lives under `~/.clauderig`, outside both configured roots
  (`$HOME/.claude` and the Desktop application-support directory), and a
  regression test asserts that: a profile store *inside* a root would be swept
  wholesale by that root's own walk, with no allowlist in the way.
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

## Backup and restore

`clauderig sync` covers every profile. A profile has exactly the shape of the
machine-wide install — same session trees, same `config.json` — so it is handed
to the sync engine as a **root of its own**, staged under `desktop@<name>`:

```console
$ clauderig sync
  cli                 412 files, 0 secret field(s) redacted
  desktop             19 files, 0 secret field(s) redacted
  desktop@work        8 files, 0 secret field(s) redacted
  desktop@personal    8 files, 0 secret field(s) redacted
```

Same walk, same allowlist, same retention window, same redaction pass, same
sidecar pruning as the machine-wide root. No second tree format to reason about,
and nothing new to turn on.

**Sync never writes inside a profile.** It reads, and the allowlist is
include-only, so a profile contributes nothing beyond what the unprofiled
Desktop root already contributed — the named config and session-metadata files,
and nothing else. Cookies, Local Storage, the token cache and the OAuth blobs
in `config.json` are not in that set on any platform.

`restore` does write into a profile — that is the whole point of it, and it
happens only when you ask for it, never as a side effect of syncing. It can
only put back what sync took, so a restored profile comes back with its
settings and its history and is still **signed out**: the login was never in
the backup to begin with. Restored files carry the profile store's own modes
(0700 directories, 0600 files on Unix), so a profile recreated on a fresh
machine is as contained as one `clauderig desktop add` made.

Alongside the app's `data/` tree, a profile's own `profile.json` — clauderig's
record of the name, label and creation time — travels too. Without it a restore
would leave a directory of files that `clauderig desktop open` could not find.

**Restoring onto a machine that has never run `clauderig desktop`** is the case
that matters, and it works: the profile list comes from the staging tree rather
than from local state, and locations are resolved from a `$HOME`-relative
template rather than from the profile store, so they resolve on a computer that
has never seen the profile. `restore` recreates each one, `clauderig desktop
list` shows it, and one login per profile picks up where the old machine left
off.

**Turning it off** is the Desktop root's `enabled` flag in `clauderig config`.
Profile roots inherit it: disabling Desktop sync is a statement about Desktop's
data, and profiles are more of it, so one switch covers both rather than a
second one nobody would think to look for.

### Why history is not pooled across profiles

An earlier version of this feature (`clauderig desktop share`, removed before
release) pointed several profiles' session directories at one tree, so a Claude
Code chat started in any window appeared in all of them. It worked. It was
withdrawn anyway:

- It was the **only** place clauderig wrote inside a profile — the one thing
  this whole model promises it will not do. Everything above stops being true
  the moment that promise has an exception.
- Desktop partitions those trees by account uuid deliberately. Merging them
  pools genuinely account-scoped state (`scheduled-tasks.json` and friends) and
  quietly moves work onto whichever account's window happens to be open.
- It stood on a directory layout the app is free to change in any release, and
  the failure mode would be silent.

The value people actually wanted from it was **backup coverage**, and that
needs none of it — hence this section. For reading history across accounts,
`clauderig search` already spans every synced transcript without moving a file.

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

**Shortcuts.** The macOS bundle is covered end to end: the tests build one and
*run* it against a stub binary, asserting it invokes `clauderig desktop open
<profile>` with the arguments intact through a path containing a space and a
quote. The alert path is checked by parsing the script (`sh -n`) rather than by
running it, because running it puts a modal dialog on the screen of whoever is
running the tests. The Windows side creates real `.lnk` files through PowerShell
and reads the fields back, and those tests run on the `windows-latest` CI
runner — but the click itself has not been tried by hand on Windows. Worth
adding to the manual pass above:

```console
clauderig desktop shortcut scratch --to desktop --to apps
# click it: a window opens on that profile
# click it again: the same window comes forward, no second instance
clauderig desktop shortcut scratch --rm
```
