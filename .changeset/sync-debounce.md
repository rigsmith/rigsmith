---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Hook-driven syncs now debounce and take a lock, instead of running at the end of every turn in every chat.

The `Stop` hook fires when a turn ends, and it fired a full sync each time: walk the tree, redact every JSON file, commit, push. Measured on a real machine with **one** conversation open — 37 syncs, a median gap of 163 seconds, and a minimum gap of **7 seconds** — to write one changed file against 3,260 unchanged ones. Several chats at once multiplied that and raced each other on the same git repo.

The installed hook is now `clauderig sync --hook`, which does two things a bare sync does not. It takes a lock beside the staging repo, so a second sync that starts while one is running steps aside rather than contending — the run already in flight is walking the same tree and will capture the same work. And it skips if a sync completed within `hookIntervalSeconds` (default 300, i.e. five minutes).

`clauderig sync` typed by hand is never debounced and never skipped. Someone asking for a sync now means now.

The interval is a trade and it is worth stating: the last turn before you walk away may not be backed up until something triggers the next sync. That is why the default is five minutes rather than an hour, and why `hookIntervalSeconds: -1` turns the debounce off entirely and restores the old every-turn behaviour.

The last-sync time is read from the journal rather than a stamp file of its own — the journal already records exactly this, is already bounded, and cannot drift from what the activity feed shows. Failed syncs do not count toward the interval, so a machine that cannot push is never made to wait before being allowed to try again.

The lock lives beside the repo rather than inside it, so it never shows up as an uncommitted change, and a lock older than twenty minutes is broken and taken: a sync killed mid-run must not stop syncing forever, and two overlapping syncs are something git's own `index.lock` already handles.

**Existing installs keep firing on every turn until the hook is rewritten** — run `clauderig global install` to pick up the new command.
