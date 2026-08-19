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
| `clauderig desktop add <name> [--email X]` | Create a profile and open a fresh window to log into. One login, once. |
| `clauderig desktop open <name\|email>` | Open the profile's window, or focus it if already open. |
| `clauderig desktop list` (alias `ls`) | Saved profiles; `●` marks the ones open right now. |
| `clauderig desktop quit <name\|email>` | Close that profile's window (SIGTERM, then firmly). |
| `clauderig desktop rm <name\|email> [--force]` | Delete the profile. Signs that account out of Desktop for good. |
| `clauderig desktop map [<name>] [dir]` / `unmap [dir]` | Bind a directory to a profile, so a bare `desktop open` there opens it. Bare `map` lists every binding. |

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

## Known limitations, and the next step

**Chat history is per profile.** Each profile has its own
`claude-code-sessions` and Cowork session directories, so a Claude Code session
started from the `work` window does not appear in `personal` — and clauderig's
sync only sees the *default* Desktop root, so profile history is not synced at
all today.

guise solves this by symlinking each profile's `claude-code-sessions` at one
shared folder. That is the right shape for clauderig too, and would make the
existing Desktop sidecar sync cover every profile at once, but it needs care
before it ships:

1. The symlink may only be repointed while that profile's window is **closed** —
   Electron will happily write through a link it opened before the swap.
2. Existing contents must be migrated into the shared folder, not orphaned.
3. Windows needs a junction (or Developer Mode) rather than a symlink;
   `os.Symlink` on a directory there fails without privileges.
4. `clauderig sync`'s allowlist and secret tripwire must be re-checked against
   the merged tree — several accounts' session metadata in one folder is a
   different shape than the one the retention rules were written for.

Proposed as `clauderig desktop share` (opt-in, off by default) once the above is
settled.

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
