---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`redactTranscripts` now scrubs the whole conversation, not only the transcript file.

A credential lands wherever the conversation put it. Claude Code writes large tool output to `tool-results/*.txt` beside the transcript, and notes end up under `memory/` — so a key printed by a command, or pasted into a note, was never covered. Scrubbing matched `projects/**/*.jsonl` and nothing else.

That left the setting looking like it worked while the sync stayed blocked: the tripwire reads everything, found the bearer tokens in four tool-result files on one real machine, and refused — with the scrubber switched on and nothing left for its owner to try.

Staged text under `projects/` is now scrubbed whatever its extension: `.jsonl`, `.txt`, `.md`, `.html`. Structured `.json` still goes through the field-level redactor, which knows where a value ends, and binaries are left alone — a byte-level rewrite of an image is not a redaction, it is damage. The live files in `~/.claude` are still never touched.

Worth stating plainly: this cleans what is **published from now on**. It does not reach into commits already pushed. History that already carries a credential needs `clauderig repo prune --before <date>` to drop it, or a rewrite and a reset on every machine.
