---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Review follow-ups to the two credential-scanning fixes, both of which left a hole.

**The rewriter and the tripwire disagreed.** The exclusion for hyphenated prose landed in the code that rewrites credentials but not in the code that refuses them, so the tripwire kept refusing exactly the phrases the rewriter had decided to leave alone. That is the worst version of the bug: the scrubber on, the sync still blocked, and nothing left for its owner to try. Both now ask one shared question.

**Scrubbing was decided by file extension.** That gets both ends wrong. Tool output written to a `.log`, or to a file with no extension at all, is text that would keep its credential and keep the sync refused; a PNG somebody named `.md` would be handed to the rewriter, which would edit bytes inside an image. The decision is made on the file's content now, using the same check the scanner already applies.

Also: a sentence in the configuration docs was left as a broken fragment by the previous edit, and a test discarded a read error in a way that would have reported "the live note was rewritten" when the real problem was that the note could not be read.
