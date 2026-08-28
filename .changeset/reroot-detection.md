---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

New `internal/clauderig/reroot`: works out when a session is filed under a directory it never really worked in.

A session is filed under the directory Claude Code was launched in, and the agent then goes wherever the work is. On a real machine **65 of 120 recent transcripts recorded more than one cwd**. The result is a chat filed under `~/Git` — a folder nobody works in — when everything it did happened in one project underneath.

Deciding which of those is actually misfiled turned out to be the whole problem, and two obvious rules are both wrong:

- **Most common cwd.** A session launched at a repository root that spent most of its time in `ui/` is not misfiled; the repository is the unit a session belongs to. This rule re-files it under its own subfolder.
- **Does the directory contain a `.git`.** Wrong in both directions. It excludes plain folders that are unmistakably projects — the real case found here was a project directory with no repository in it at all — and it includes every checkout that happens to sit under another one.

What works is asking the sessions rather than the filesystem: a directory that several *distinct* projects sit directly inside is somewhere you cd through, not somewhere you work. On this machine that finds `~/Git` holding 23 projects and two worktree roots holding 56 and 16, and no repository at all.

A move is suggested only when the filed directory is one of those holders, most of the work happened elsewhere, and the directories it did work in share a single common ancestor underneath. When the work genuinely spanned sibling projects the launch directory is the honest answer and nothing is offered.

Measured over 688 real transcripts, the naïve version flagged 49 — most of them proposing to re-file a project under its own subfolder. This one flags 1, and it is correct. That ratio is the point: a suggestion that offers to rewrite a transcript has to be right much more often than it is convenient.

Detection only for now; the move itself is not yet wired up.
