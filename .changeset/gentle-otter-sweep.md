---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Unblock syncing when transcripts hold credentials the scrubber never got to.

Three faults compounded into a sync that refused on every run and re-did all of
its work each time:

- A transcript quoting a PEM private key was refused rather than scrubbed, even
  though the scrubber gained a rule for exactly that shape. The refusal now
  applies only where it has to — a key block in raw text, whose body runs on
  into lines a line-at-a-time rewrite would copy through untouched. Inside a
  JSON record, which is what every transcript line is, the block is scrubbed.
- The marker recording what a run scrubbed was written past the tripwire, so a
  refused run never recorded it. Every later run then re-scrubbed every
  transcript in the tree — thousands of files — before refusing on the same
  finding. It is now written once the staging pass is done, whatever the
  tripwire goes on to say.
- A staged transcript whose live source is gone (a deleted worktree, an
  archived project) was never walked again, so it kept whatever it held when it
  was staged. If that predated redaction, no setting could ever clear it. The
  run that turns redaction on now scrubs those staged copies in place.
