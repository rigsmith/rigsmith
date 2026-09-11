---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

A refused sync now says what was caught, in a sentence that agrees with its own count.

`clauderig status` reported `Refused to push — 1 value look like credentials`, which is ungrammatical at the commonest count there is, and wrong about what happened: the journal entry for the same event said `1 file(s) are credential material`. One sentence had a plural verb beside a singular noun; the other named a different thing entirely.

The distinction is not pedantry. A *value* means the redactor's key rules missed something inside a file worth syncing; a *file* means something is in the allowlist that should not be. They send you to different places, and the summary was naming the wrong one — so the sync report now carries how many findings were whole files, the journal records it, and both front ends render through one helper rather than each writing their own sentence about the same record.

Records written before this have no such count and read as values, which is how they were always rendered and the only honest answer for a record that never drew the distinction.