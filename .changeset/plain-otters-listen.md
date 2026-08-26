---
"github.com/rigsmith/rigsmith": minor
---

`clauderig recent` lists your Claude Code sessions newest first with no search term — the answer to "I was working on it yesterday, what was it called?". Each line shows the client that ran it (vscode / desktop / cli / sdk-\*), the title, the git branch it ended on, and the project; `--since` (24h by default), `--until`, `--cwd` and `--account` narrow it, and `-l` prints full ids with ready-to-run resume commands.

Sessions are now dated by the timestamp on their **last transcript record** rather than by the file's mtime — in `recent`, in `search`'s ordering and `--since`/`--until` filters, and in the rows the ledger records. mtime does not survive being copied: a restore, a checkout of the synced repo, or any tool that walks `~/.claude` rewrites hundreds of transcripts to the same minute, which re-dates old chats to today and buries the ones you actually used. The record timestamps are content, so they survive all of it. A transcript that carries no timestamped record is still listed, marked `~`, rather than being silently believed or silently dropped.

`recent` also takes an optional search term (`clauderig recent webhook`) that narrows the window by title or body while keeping time order — it reads only the transcripts inside the window, so it answers immediately, which is the difference from `search` ranking the whole store by relevance.

`search` and `recent` now read the Desktop sidecars of **every** `clauderig desktop` profile, not just the machine-wide install, and label each session with the profile that owns it. Several Claude Desktop installs can share one machine, they all write `entrypoint: claude-desktop` into the same `~/.claude/projects` tree, and previously two thirds of the sidecars on a three-install machine went unread — so those sessions showed up with no title, no model, and nothing to say which app to reopen them in.

Which Desktop profile owns a session is now read from the `accountUuid` its sidecar is filed under — the same ground truth `ledger.AccountFromDesktop` trusts — rather than from whichever profile's tree a copy of the file turned up in. Sidecars get copied between installs and keep their account path, so the tree is not ownership: on one real machine all 25 shared sidecars sat in the other account's tree. `-l` now names the profile to open (`clauderig desktop open <name>`) for a Desktop-only session, because Claude Desktop lists only the sessions filed under the account it is signed in as — no other install will ever show it.

As a side effect the ledger's change fingerprint stops moving when a transcript is merely copied, so a restore no longer forces a rewrite of every row on the next sync.
