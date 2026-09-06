---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`redactTranscripts` can now clear everything the tripwire refuses on.

Two credentials could be detected and never removed, so a sync refused for ever with the scrubber already on and nothing left for its owner to try.

A **PEM private key** was reported on its header alone, while the scrubber declined to rewrite PEM at all — so a transcript that merely quoted one blocked the machine's backups permanently. Key material is now removed from the header to the end of the string holding it, which covers the truncated case too: one file here carried four `BEGIN` markers and no `END`, because a transcript records what was on screen. The surrounding record stays valid JSON.

A **JWT written straight after an escape** — `…turso.io\neyJ…` — sat behind a word character, and the word boundary added to the signatures in 1.15.0 made the scrubber skip it while the scanner still saw it. A JWT's own shape is three dot-separated base64url runs, which prose does not produce, so that rule needs no boundary and now has none.

A test now holds the rule behind both: anything the scanner detects, the scrubber must be able to remove. On the machine that found this, 26 flagged files go to none.
