---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

The clauderig UI has a second window: a sessions manager listing every Claude Code session this machine can see, wherever it lives. Open it from the tray menu (**Sessions…**), or start the app with `--sessions`.

One row per session, merged across the places a session actually leaves a trace — this machine's live `~/.claude`, each Claude Desktop install including clauderig-managed profiles, and the synced staging repo. Each row shows how long ago you used it, the project directory and the branch it ended on, the account it belongs to, the client that ran it (`cli`, `vscode`, `desktop@profile`, an `sdk-*`), its title, and the last thing you typed in it.

The title and the last prompt are separate columns on purpose. The title is the first thing you asked, which is what a session was opened to do; an hour later that is rarely what it became. The last prompt is what makes a row recognisable when you are hunting for the chat you were just in.

A "where" column names which stores hold a transcript, because the lopsided rows are the ones worth seeing: `repo` alone means the session is backed up but not on this Mac, and `cli` alone means it exists only here and has never been synced. The footer says what the list could not cover — sessions with no readable date, sessions with no recorded account, and any machine whose sync is stale enough that its recent work is not listed. A session finder that quietly shows less than it should is worse than one that says so.

Dates come from each transcript's own last record rather than the file's mtime, so restoring a backup or checking out the synced tree does not re-date every chat to the same instant. The few that can only be dated from a file or an old ledger row are marked `~`.

Click a row and a panel opens with the first and last couple of prompts, so you can tell one session from another without opening it, plus the things you actually want to do next.

**Resume** works two ways. *Open in terminal* runs the resume command for you; *Copy command* hands you `cd <project> && claude --resume <id>` for any terminal, multiplexer or other machine. *Open in Desktop* imports the session into a Claude Desktop profile you pick — only clauderig-managed profiles are offered, never the machine-wide install, because sending a session there files it under whichever account that install happens to be logged into. All three are disabled unless the transcript is in this Mac's `~/.claude`, since that is the only copy any of them can read.

**Delete** asks first, and asks properly: the dialog lists the stores that actually hold the session and you tick which to remove, defaulting to this Mac only. It tells you which way the asymmetry falls — keeping the synced copy means a restore can bring the session back, removing it means the deletion reaches your other machines on the next sync. A session a running Claude Code process is writing to is refused outright rather than deleted underneath it. The ledger's record that the session existed is kept; only the conversation goes.

**Search** starts with the box at the top, which matches anything a row shows — title, last prompt, project, branch, id, client. When that finds nothing, the empty state offers to look deeper, and **search inside** (or just pressing Enter) runs the same content scan `clauderig search` does, over the transcripts themselves. Matching rows show the hit count and the first snippet in place of the last prompt, because the hit is why the row is there. Both filter server-side, so they search the whole window rather than the rows that happen to be loaded.

**Open** reopens a session where it was last used, read off the client its transcript recorded — `Open in Terminal`, `Open in VS Code`, `Open in Desktop · <profile>` — with the alternatives behind an `Other…` picker beside it. VS Code resumes the actual session rather than just opening the folder: the extension registers a `vscode://anthropic.claude-code/open?session=…` handler, so the window opens the project and then hands it the session. All of them need the transcript to be on this Mac, since that is the only copy any of them can read.

The status window gains a **Sessions** button, both windows can be dragged by their header, and `--sessions` opens the manager at startup the way `--window` opens the status window.

Underneath, the listing moved out of the `recent` command into a package both front ends read, so `clauderig recent` and the window answer with the same facts rather than two implementations that drift.
