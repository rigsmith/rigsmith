# brewRig

Keep two (or more) Macs carrying the same Homebrew software, and the same
versions of it — without either machine being able to silently uninstall
something on the other.

```sh
brewrig init      # name this machine, pick a private repo, take the first snapshot
brewrig status    # what's missing here, what's outdated, where versions differ
brewrig apply     # install what's missing
```

## Why not a shared Brewfile

`brew bundle dump` into a synced folder is the obvious answer. It breaks in three
specific ways:

- **Last-writer-wins.** Whichever Mac dumped most recently is "the truth", and
  the other machine's ad-hoc installs are invisible until they are overwritten.
- **`--cleanup` is the only removal story, and it is all-or-nothing.** It
  uninstalls everything not in the file.
- **It cannot express intent.** A Brewfile cannot distinguish "the other machine
  hasn't installed this yet" from "the other machine deliberately removed it".
  Those need opposite responses.

brewRig syncs no Brewfile. Each machine publishes its own inventory to
`machines/<name>.json` in *your own private git repo*, and everything else is
derived. A machine only ever writes its own file, so two machines syncing at the
same moment touch disjoint paths.

## What it publishes

Only what you **asked for** — never the dependency closure. The signal is
Homebrew's `installed_on_request`, not `brew leaves`.

They are not the same set. On the machine brewRig was built against, 41 packages
were installed on request out of 143 installed; `brew leaves` returned 39,
missing `ffmpeg` and `python@3.14`. Both had been installed deliberately and both
later became a dependency of something else, so `leaves` — which means "nothing
depends on this" — stopped listing them. Syncing by `leaves` silently drops a
tool the moment anything else picks it up.

## Additive, with exactly one removal case

The union of every machine's inventory is the target. Anything in it that a
machine lacks gets installed. **Nothing is uninstalled to make machines match** —
a package only one Mac has is not drift, it is something the other hasn't caught
up on.

That leaves the real question: you deliberately `brew uninstall`ed something.
Under a pure union that decision can never propagate, and your own next `apply`
reinstalls it.

So a drop is recorded as intent. A sync that notices a package you published
before and no longer have writes it to `retired` with a timestamp, and the other
machine offers it — **one prompt per package, naming who removed it and when, and
never in a non-interactive run**. Answer "keep" and that refusal is published,
stamped with the retirement it answered, so you are not asked again — while a
later, separate retirement of the same package still is.

A reinstall beats a retirement, by timestamp. Without that comparison the two
machines deadlock: A uninstalls, B still has it, B reinstalls it on A, A
uninstalls again — forever, with neither machine wrong.

## Versions, the other half of "in sync"

Being in sync is not only *which* software is installed but *which version*.
`brewrig status` separates three things, because they have three different fixes:

| Report | Meaning | Fix |
|---|---|---|
| **missing** | in the union, not installed here | `brewrig apply` |
| **outdated** | a newer version exists upstream | `brewrig update` |
| **skew** | the machines are on different versions of the same package | `brewrig update` on whichever is behind |

Skew is the one a Brewfile cannot show you at all, and it is the usual reason two
machines "have the same things installed" and still behave differently.

## Deliberate divergence

One Mac genuinely shouldn't have something? `brewrig skip <package>` records it,
and the decision is **published** so the other machine stops proposing it rather
than asking forever. Installing the package normally undoes the skip — an
install always beats a skip.

## Not Windows

Homebrew doesn't run there, so brewRig ships darwin and linux only. It is the one
rig that isn't on all three platforms, and that is a property of Homebrew rather
than a gap to fill later.
