---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig reroot <session-id> <dir>` re-files one session under a directory you name, and the window has a button for it.

Claude Code files a session under the directory it was launched in. Start in a folder that merely holds your projects, work in one of them, and the conversation is filed under the folder — so `claude --resume` opens it in the wrong place and it shows up in the wrong project's history. Measured here, 65 of 120 recent transcripts recorded more than one working directory.

You name the session and you name the directory. Nothing is inferred, suggested or defaulted: which directory a conversation belongs to is a judgement only the person who was there can make.

The mechanics are the point. Records recorded at the session's old root are rewritten to the new one, and the transcript moves into the project directory that root flattens to. **Records from deeper directories are left alone** — this is a re-root, not a rebase: nothing moved on disk, so a record naming `~/Git/thing/src` still names a real directory that is still there. `mv` remains the command for when a directory has genuinely moved and everything under it moved with it.

It refuses to overwrite a transcript already filed at the destination, since that would be the same session filed twice and one conversation would be lost. It refuses while that session is running, matched on session id rather than on directory — several conversations run out of one folder at once, and blocking the move because a *different* chat is open there would be refusing for no reason. `--dry-run` reports how many records would change without touching anything, and the window's dialog always runs one first, so the confirmation names a number you agreed to before a file was written.

The next `clauderig sync` carries the change to your other machines.
