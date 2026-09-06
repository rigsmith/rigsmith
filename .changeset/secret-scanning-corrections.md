---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

The secret tripwire no longer refuses a sync over ordinary English.

1.14.0 started scanning every staged file for credentials, and the patterns it used were never anchored — `sk-` is the tail of **task-**, and `AKIA` matched inside any long uppercase run. On one machine that was 96 of 134 findings, and the sync had been refusing since, so it was not being backed up at all. Turning `redactTranscripts` on could not help: it scrubbed only `.jsonl` files, while the tripwire read everything, and the two disagreed about what counted as a credential.

If your sync started refusing after 1.14.0, this is why, and upgrading is the fix.

`redactTranscripts` now scrubs the whole conversation — the transcript, the tool results written beside it, and notes under `memory/` — deciding text from binary by content rather than by file extension. Your live `~/.claude` files are still never modified.

It cleans what gets published from now on. It does not reach into commits already pushed; history that carries a credential needs `clauderig repo prune --before <date>`.
