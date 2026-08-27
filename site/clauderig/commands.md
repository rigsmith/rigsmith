# Commands

| Command | What |
|---|---|
| `init` | First-run wizard: remote (private), machine identity, roots, hooks |
| `sync` | Walk → redact → manifest → tripwire → commit → push (`--dry-run`) |
| `pull` | Fetch latest into the staging repo (no write to `~/.claude`) |
| `restore` | Restore here, rewriting paths (`--dir`, `--backup`, `--force`, `--prune`); nudges a Desktop restart when Code sessions come back |
| `status` | Sync state: remote, last sync, roots, hooks |
| `search` | Find a Claude Code session by title or content across live + synced history (alias `grep`); `--since`/`--until`/`--cwd` narrow, `--raw` grep lines, `--all` every file, `--live`/`--repo` scope, `-s` case-sensitive |
| `recent` | List sessions newest first (alias `last`); dated by each transcript's own records rather than by file mtime, and labelled with the client + Desktop profile that ran each one. Takes an optional search term to narrow the window. `--since` (default `24h`) / `--until` / `--cwd` / `--account` narrow, `--limit` caps, `-l` adds resume commands |
| `ledger` | Report the permanent session index; `ledger backfill` recovers rows for sessions pruned before it existed (`-n` dry run) |
| `global` | `install` / `uninstall` / `status` the global sync hooks in `~/.claude` (alias `hooks`) |
| `project` | `install` / `uninstall` / `status` this repo's guard hook + CLAUDE.md guide (committed) |
| `local` | same as `project`, but gitignored (`.claude/settings.local.json`) |
| `guard` | The PreToolUse hook that enforces worktree/PR discipline — invoked by Claude Code, not run by hand (wired in by `project`/`local`) |
| `guide` | `install` / `uninstall` / `status` / `show` the CLAUDE.md guide block standalone (`--global` targets `~/.claude/CLAUDE.md`, `--path` overrides; `install` previews in a scrollable UI, skipped with `-y` or off a TTY) |
| `mcp` | `list` (alias `ls`) / `get` / `add` / `remove` (alias `rm`) / `enable` / `disable` MCP servers (`--scope user｜project｜local`, `--transport stdio｜http｜sse`, `--env`, `--header`); bare `mcp` on a TTY opens an interactive screen (mirrors `claude mcp`) |
| `account` | Manage multiple Claude Code logins: `add` / `list` (alias `ls`/`status`) / `run <id｜email> [-- claude args]` / `switch` / `sessions` (alias `ps`) / `remove` (alias `rm`) / `purge`. `run --no-share` isolates a session; `switch` takes `--dry-run` / `--force` / `--kill` |
| `config` | `get` / `set` / `show` / `path` / `edit` (`~/.clauderig/config.json`) |
| `doctor` | Health-check environment + sync + worktree discipline (`--fix` repairs) |
| `ui` | Interactive dashboard |

The worktree and prune verbs (`rig worktree`, `rig prune`) live in
[`rig`](/rig/verbs) — claudeRig wires the *guard* that makes them the default
path. See [Worktree discipline](#worktree-discipline) below.

## The sync → restore loop

`sync` snapshots your `~/.claude` setup, redacts secret-bearing fields, rewrites
machine-specific paths into a portable form, and commits/pushes to your private
repo. `restore` does the inverse on another machine: it pulls, rewrites the
portable paths into this OS's slugs, and merges — keeping any local secrets in
place so a new machine simply re-authenticates.

When a restore brings back Claude **Code** sessions, it reminds you to fully quit
and reopen Claude Desktop — Desktop only rebuilds its Code-tab list from the
restored session sidecars on startup, so a running app won't show them until it
restarts.

## Finding a session

### "I was working on it yesterday"

When you can't remember a word from the chat but you know roughly *when*,
`recent` lists sessions newest first with no search term:

```sh
clauderig recent                    # the last 24 hours
clauderig recent webhook            # …narrowed to sessions that mention it
clauderig recent --since 7d --cwd acme-api
clauderig recent -l                 # full ids and resume commands
```

```text
  today 09:12    a1b2c3d4  vscode           Refactor the auth middleware   feat/auth-split  ~/Git/acme-api
  yest. 17:40    9f8e7d6c  desktop@work     Fix the stale README table     main             ~/Git/acme-api
  ~yest. 13:22   f4501175                   (untitled session)
```

Each line is **the client that ran it**, the title, **the git branch it ended
on**, and the project. Two of those columns exist because the project path so
often identifies nothing — a session started one directory up, or one driving a
worktree, tells you only that it was "in `~/Git`".

The client column is qualified by **Desktop profile**. One machine can carry
several Claude Desktop installs — the machine-wide one plus each `clauderig
desktop` profile — and every one of them writes `entrypoint: claude-desktop`
into the same shared `~/.claude/projects` tree. `desktop@work` versus
`desktop@personal` is the only thing that says which app to reopen a session in.
`recent` and `search` read the sidecars of *every* profile, not just the
machine-wide install, and take ownership from the `accountUuid` each sidecar is
filed under rather than from the directory it happens to sit in — sidecars get
copied between installs and keep their account path, so the directory is not
ownership.

This matters because **Claude Desktop lists only the sessions filed under the
account it is signed in as**. A profile can hold hundreds of another account's
sidecars and show none of them, so a session will never appear in any Desktop but
its own. `-l` says which one to open:

```text
  Desktop session in the work profile — no other Desktop will list it:
  clauderig desktop open work
```

Give `recent` a word and it narrows the window instead of ranking the whole
store, still in time order — it reads only the transcripts inside the window, so
it answers immediately:

```sh
clauderig recent --since 3d "connection pool"
```

#### Why not just sort by mtime

Because a file's mtime is not when you had the conversation. Restoring a backup,
checking out the synced repo, or any tool that walks `~/.claude` rewrites it — on
one real machine, **580 of 670 transcripts** had an mtime more than an hour newer
than their last message, 541 of them stamped with the same minute by a single
restore. Sorted that way, "most recent chat" means "most recently copied", and
the handful you actually worked on yesterday are buried under hundreds that only
look fresh. Claude Desktop's own session list is rebuilt from those same files
and drifts the same way.

`recent` dates each session by the **newest timestamped record** in its own
transcript. That is content, not metadata, so it survives every copy, sync and
restore. A session with no timestamped record at all — `~/.claude` holds a few
stub files that never held a conversation — is still listed, but marked `~`, so
its date is never mistaken for the real thing.

### "I remember what it said"

When you remember the words, `search` finds it by **title or content**:

```sh
clauderig search "auth refactor"
```

It scans your Claude Code transcripts (`projects/**.jsonl`) across this machine's
live `~/.claude` **and** the synced staging repo — which may hold sessions from
other machines or older ones no longer live here — then groups the hits into
named sessions:

```
● Refactor the auth middleware
  a1b2c3d4 · 2026-07-02 · opus-4-8 · ~/Git/acme-api · desktop+repo
  17 match(es)   resume: cd ~/Git/acme-api && claude --resume a1b2c3d4-…
    …split the token check out of the request handler…
```

Each result carries the session's Desktop **title**, project, last-used date, and
a ready `claude --resume` command. Titles are matched too, so a topic word finds
the chat even when it isn't in the body, and results rank by relevance (title
hit, then match count, then recency). Matches inside injected skill-listing and
system records are ignored — so a word that appears in the skill catalog (like a
skill name) doesn't light up every session.

- `--since` / `--until` — narrow to when the session was **last used**: a day
  (`2026-08-17`), an RFC3339 timestamp, or an age (`7d`, `36h`, `90m`). Days are
  read in UTC, matching the date printed on each result, and a day given to
  `--until` covers that whole day
- `--cwd` — narrow to sessions whose project directory contains this text
- `--raw` — grep-style line output instead of grouped sessions
- `--all` — search *every* file (config, skills, file-history, the Desktop dir),
  not just transcripts (implies `--raw`)
- `--live` / `--repo` — restrict to this machine or the synced repo
- `-s` / `--case-sensitive`

The narrowing flags describe *sessions*, so they can't be combined with `--raw`
or `--all` (a date isn't a property of a grep line) — `search` says so rather
than ignoring them. Sessions dropped by a filter are counted in the footer, and
one that has no date at all is dropped by a time window rather than waved
through, with its own count — so the totals always add up.

### Sessions whose body has aged out

The synced repo is a rolling window: `sync` drops project transcripts older than
`retention.historyDays` (90 by default). Without help, `search` then answers
*no matching sessions* for anything older — which reads as *that chat never
existed*, when in truth only its body left the window.

So `sync` keeps a **ledger**: `index/<device>.jsonl`, one row per session it has
ever staged — id, title (the first prompt), project, date — written *before*
retention runs, and never deleted. A row is a couple of hundred bytes; a
several-hundred-session history is well under a megabyte.

Those sessions still answer a search:

```
● the auth refactor
  0f3a91c2 · 2026-03-04 · ~/Git/api · ledger
  title match   aged out of the synced window — body recoverable from the sync repo's git history
```

Only the **title** is searchable for them — the body isn't here to scan — and
the note is deliberately not "gone": the blob is still in the sync repo's git
history, which is the whole reason the row was kept. A session whose body *is*
still staged is never labelled this way; it gets the ordinary
*synced copy only — restore on this machine to resume*.

One file per device, because two machines appending to a shared file is a merge
conflict on every sync. Rows are keyed by session id and unioned on read, newest
row winning.

#### Backfilling what aged out before the ledger existed

The ledger only remembers from the day it is installed — every session pruned
before that has no row. Their bodies are still in the sync repo's history
though, because a deleted file leaves a git tree, not the history behind it:

```sh
clauderig ledger backfill        # -n / --dry-run to see what it would recover
clauderig ledger                 # what it remembers now
```

`backfill` finds every transcript retention has removed, reads each one's head
from the commit *before* its deletion, and writes the row. Rows already present
are left alone — a live transcript is a better source than a deleted blob — so
running it twice does nothing, and there is no reason to run it more than once.
It writes into the staging tree; your next `sync` commits it.

Recovered rows carry a date from the last commit that touched the transcript,
which is when it last changed rather than a timestamp read out of the body.

::: warning What history still holds
`sync` squashes the staging repo once it passes the size floor, and a squash
prunes unreachable blobs — so bodies dropped before the last squash are gone,
and `backfill` can only recover what history still carries. That is also why a
ledger-only result says the body *may* still be recoverable rather than
promising it.
:::

### Which machines the search could see

Absence of a hit is the answer people act on — *that chat is gone* — and it only
holds if the store is complete. A machine that hasn't synced since Tuesday makes
every Wednesday session invisible here, so `search` closes with the device
roster and flags any machine it couldn't see:

```
1 session(s) match
scanned 1276 transcripts, skipped 0 binary
devices  mbp-16 8m ago (this) · air-13 3d ago
air-13 has not synced since 2026-08-16 23:39 UTC — anything it recorded after
that is not searchable here
  run `clauderig sync` there, then `clauderig pull` here
```

In the **default** scope, only *other* machines can hide anything — this one's
live `~/.claude` is scanned directly, so its own sync age costs the search
nothing, and the roster is skipped entirely when yours is the only device (there
is then no elsewhere for a chat to be).

Two scopes change that:

- **`--repo`** searches only the synced repo, so this machine's live tree is
  *not* read: anything it has not yet synced is as invisible as another
  machine's, and it is warned about like any other device — including when it is
  the only one on the registry.
- **`--live`** takes the synced repo out of scope, and with it any claim about
  other machines, so no roster is printed at all.

If the registry itself can't be read, the footer says
`device coverage unavailable` rather than falling silent — silence there would
be indistinguishable from a verified single-machine setup, which is the opposite
conclusion.

On a long search a live `scanning… N transcripts, K matches` status shows on the
terminal (and stays out of piped output).

::: tip Desktop "Chat" tab
Desktop **Chat**-tab conversations live server-side, not on disk — they never
appear here. `search` covers your Claude **Code** transcripts; a miss doesn't
prove a Chat-tab conversation is gone, so check claude.ai for those.
:::

### Opening one in Claude Desktop

Having found a session, `clauderig desktop open --session` carries it into
Desktop's Code tab:

```sh
clauderig desktop open work --session a1b2c3d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d
clauderig desktop open work --session "auth refactor"   # title or project text
clauderig desktop open work -i                          # pick one from a list
```

You rarely know a uuid, so `recent -l` prints the command under each session,
beside the terminal one — for the sessions it would work on, which means Claude
Desktop installed, a profile saved, and a transcript in `~/.claude/projects`
here:

```text
● Refactor the auth middleware
  a1b2c3d4 · 2026-05-14 · desktop@work · opus-5 · ~/Git/acme-api
  resume: cd ~/Git/acme-api && claude --resume a1b2c3d4-…
  desktop: clauderig desktop open --session a1b2c3d4-…
```

`--session` also completes to recent ids on `<Tab>`, each labelled with its title
and project, and `-i` on its own opens a picker over the same list.

clauderig does not import anything itself: it hands Desktop a
`claude://resume?session=<uuid>` link and the app's own handler reads the
transcript. That is why the transcript has to be in `~/.claude/projects` on this
machine — a session that lives only in the synced repo must be restored here
first. It is also why a second open profile makes the command refuse: a deep link
is routed by scheme rather than to a window, so the OS would pick the recipient
and could file the session under the wrong account. Quit the others, or pass
`--anyway` when any window will do.

The profile model this sits on is in
[`docs/CLAUDERIG-DESKTOP-PROFILES.md`](https://github.com/rigsmith/rigsmith/blob/main/docs/CLAUDERIG-DESKTOP-PROFILES.md).

## Hooks

```sh
clauderig hooks install
```

Wires two Claude Code hooks: **SessionStart → pull** (so each session starts
from the latest synced state) and **Stop → sync** (so your work is captured when
a session ends). Both are portable across OSes and idempotent.

## Worktree discipline

`clauderig guard` (a PreToolUse hook) and `rig worktree` make worktrees and
PRs the default path for Claude Code, and stop a session from scrambling your VS
Code chat history by moving its working directory. Chat history is keyed to the
folder path, so the model edits from one pinned window while worktrees open in
their *own* window for review only.

```sh
rig worktree new <branch>   # sibling checkout off mainline (prints the path)
rig worktree new <branch> --open    # …and open a review window for this run
rig worktree new fix/x --base release-1
rig worktree list           # this repo's worktrees (alias: ls)
rig worktree open <branch>  # (re)open a worktree's review window (branch or path)
rig worktree rm <branch>    # remove the worktree, keep the branch (-f if dirty)
```

Worktrees live at `<parent>/<repo>-worktrees/<branch>` — a **sibling** of the
repo, so they never clutter the primary checkout and each gets its own
review-window history. `new` never moves the session's cwd; it prints the path
and, when opted in, opens a separate window.

### Configuring the review window

Because `worktree` is a [`rig`](/rig/verbs#git-worktree-verbs) command, the
review-window behavior is configured in **`.rig.json`** via `rig config set`, not
in claudeRig. By default `new` does **not** open a window — opt in per run with
`--open`, or always with the `worktree.autoOpen` key:

```sh
rig config set worktree.autoOpen true       # always auto-open (like --open)
rig config set worktree.openCmd "cursor -n"  # open Cursor instead of VS Code
```

- **`autoOpen`** (default `false`) — whether `new` opens a window at all.
  `--open`/`--no-open` override it per run; `worktree open` always opens.
- **`openCmd`** (default `code -n`) — the program plus any flags; the worktree
  path is appended as the final argument and run directly (no shell). Examples:
  `code -n`, `cursor -n`, `code-insiders -n`, `subl -n`, `idea`.

See [rig → Configuration](/rig/configuration#worktree) for the full details.
When the opener isn't on `PATH`, `new`/`open` print the command to run instead.

::: tip
See the [worktree-discipline doc](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/WORKTREE-DISCIPLINE.md)
for the guard rules and the full model.
:::

::: tip
See the [design doc](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/CLAUDERIG-DESIGN.md)
for the full picture.
:::
