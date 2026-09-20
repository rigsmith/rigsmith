# brewrig — design

Keep two (or more) Macs carrying the same Homebrew software, and the same
versions of it, without either machine being able to silently uninstall
something on the other.

The sixth rig. Same north-star as the rest: one statically-linked Go binary,
zero runtime deps, config in schema-stamped JSONC, state in *your own private
git repo*.

## Why not just a shared Brewfile

`brew bundle dump` / `brew bundle install` is the obvious answer and it is the
wrong one for two machines:

- **A single Brewfile is last-writer-wins.** Whichever Mac dumped most recently
  is "the truth". The other machine's ad-hoc installs are invisible until they
  are overwritten.
- **`brew bundle --cleanup` is the only removal story, and it is all-or-nothing.**
  It uninstalls everything not in the file. That is exactly the surprise
  uninstall this tool exists to prevent.
- **A Brewfile cannot express intent.** It cannot distinguish "machine B never
  installed this yet" from "machine B deliberately removed it". Those need
  opposite responses, and the single-file model collapses them.

So brewrig does not sync *a* Brewfile. Each machine publishes **its own
inventory**, and the shared state is the set of them. Everything else is derived.

## The repo

```
README.md                    written by `brewrig init`; says what this repo is
machines/<machine>.json      one machine's published inventory — the only file it writes
```

A machine only ever writes its own file. Two machines syncing at once touch
disjoint paths, so the common case never conflicts. This is the same reason
clauderig shards per machine.

## What gets published

Only what was **asked for**, never the dependency closure. On the Mac this was
built against that is 41 formulae out of 143 installed — the other 102 are
dependencies brew resolves on its own, and syncing them would turn every upstream
dependency change into spurious drift.

The signal is `installed_on_request` from `brew info --json=v2 --installed`, not
`brew leaves`. They are not the same set, and the gap is not academic: on that
same machine `leaves` returns 39, missing `ffmpeg` and `python@3.14`. Both were
installed deliberately; both later became a dependency of something else, so
`leaves` — which means "nothing depends on this" — stopped listing them.
Syncing by `leaves` silently drops a tool the moment anything else picks it up.
`installed_on_request` records that you asked, which is the thing worth
mirroring.

One `brew info --json=v2 --installed` call (~0.75s) yields taps, formulae, casks,
versions and install times together. The whole read side is that one subprocess.

## Additive union, and the one case that removes

The union of every machine's inventory is the target. Anything in the union that
this machine lacks gets installed. **Nothing is uninstalled to make a machine
match the union** — a package only this Mac has is not drift, it is something the
other Mac hasn't caught up on yet.

That leaves a real question: you deliberately `brew uninstall`ed something on
machine A. Under a pure union that decision can never propagate — worse, A's own
next `apply` reinstalls it, because B still lists it. The union model fights you.

So a drop is recorded as intent. When a sync notices a package that this machine
published last time and no longer has installed, it writes it to `retired` with a
timestamp:

```json
"retired": { "cask:libreoffice": "2026-09-20T14:02:11Z" }
```

The key is `kind:name`, because a formula and a cask can share one (`docker`
does). A hand-written bare name does not parse and is dropped.

`retired` is what produces the only uninstall brewrig will ever propose, and it
is **always confirmed, one package at a time, never in a non-interactive run**.

Reinstalling beats retiring, by timestamp. If B still wants the package it
reinstalls, and brew's own `installed[].time` is later than A's retire stamp, so
B's inventory wins and the entry stops being offered. Without that comparison the
two machines would fight forever: A retires, B reinstalls, A's stale stamp
retires it again on the next pass.

## Versions, which is the other half of "in sync"

Being in sync is not only *which* software is installed but *which version*.
`status` reports three distinct things, because they have three different fixes:

| Report | Meaning | Fix |
|---|---|---|
| **missing** | in the union, not installed here | `brewrig apply` |
| **outdated** | newer version available upstream | `brewrig update` |
| **skew** | the machines are on different versions of the same package | `brewrig update` on whichever is behind |

Skew is the one a Brewfile cannot show you at all, and it is the usual reason two
machines "have the same things installed" and still behave differently.

## Not Windows

Homebrew does not run on Windows, so brewrig ships **darwin and linux only** — no
`windows` in its goreleaser `goos`, no winres manifest, no Scoop entry. It is the
first rig that is not on all three, and that is a property of Homebrew, not a gap
to fill later.

## Safety

- The remote must be a **private** repo, verified through `gh` — a package list
  is a decent map of your machine, and this matches clauderig's rule.
- `apply` is **transactional per package, not atomic**: brew itself cannot roll
  back, so a failed install is reported and the run continues, rather than
  leaving a half-applied state unmentioned. The summary names every failure.
- Nothing is uninstalled without an interactive confirmation naming the package
  and the machine that retired it.
