---
type: feat
scope: brewrig
"github.com/rigsmith/rigsmith"
---

brewrig: keep several machines on the same Homebrew software, and the same versions of it.

The sixth rig. Each machine publishes what it has *deliberately* installed to your own private git repo, and installs whatever the others have that it doesn't. `brewrig init` on both Macs is the whole setup.

It deliberately does not sync a Brewfile. `brew bundle dump` into a shared folder is the obvious answer and it fails in three specific ways: a single file is last-writer-wins, so the other machine's ad-hoc installs are invisible until they are overwritten; `--cleanup` is the only removal story and it uninstalls everything not listed; and a flat list cannot tell "the other machine hasn't installed this yet" from "the other machine deliberately removed it", which are the two cases that need opposite responses. So each machine writes only `machines/<name>.json` and the union is derived — which also means two machines syncing at once touch disjoint paths and do not conflict.

What gets published is `installed_on_request`, not the dependency closure and not `brew leaves`. The closure would turn every upstream dependency change into drift. `leaves` is subtly wrong in the other direction: it means "nothing depends on this", so it drops a tool you installed on purpose the moment anything else picks it up as a dependency — on the machine this was built against it silently omits `ffmpeg` and `python@3.14`, 39 packages where 41 were asked for.

Nothing is uninstalled to make machines match: a package only one Mac has is not drift, it is something the other has not caught up on. The single exception is a package you deliberately removed, which is recorded as `retired` with a timestamp and offered on the other machine one prompt at a time, naming who removed it and when — never in a non-interactive run, where it reports the count and changes nothing. Answer "keep" and that refusal is published as an `acknowledged` entry stamped with the retirement it answered, so you are not asked again — while a later, separate retirement of the same package still is. (It is deliberately not an opt-out: an opt-out means "do not install this here", and it is cleared the moment the package is installed, which it is.) A reinstall outranks a retirement by timestamp, which is what stops the two machines deadlocking: A uninstalls, B reinstalls it on A, A uninstalls again, forever.

`status` separates three things that a Brewfile collapses into one, because they have three different fixes: **missing** here (`apply`), **outdated** against upstream (`update`), and **skew** — the machines on different versions of the same package (`update` on whichever is behind). Skew is the usual reason two Macs "have the same things installed" and still behave differently, and it is invisible to a dumped Brewfile.

Unlike its siblings brewrig ships for macOS and Linux only, since Homebrew does not run on Windows: no winget or Scoop entry, and the install script skips it there instead of failing.
