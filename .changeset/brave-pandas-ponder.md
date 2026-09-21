---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`publishDirs` says something when it cannot do what you asked.

Four ways it could publish fewer packages than intended and report success anyway: a `publishDirs` written as a string rather than a list decoded to nothing and took the no-config path; a matched path that could not be inspected was skipped like a non-directory; a `package.json` that was itself a symlink out of the repository was read anyway, after the directory had been checked; and two generated directories claiming one package name silently published whichever sorted first. Each is now an error naming what it found. A configured glob that matches nothing stays a non-error — before the build that writes those directories has run, empty is correct — but it now says so, because a publish that shipped none of the generated packages otherwise looked exactly like one that had none to ship.
