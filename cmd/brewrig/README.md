# brewrig

Keep two (or more) Macs carrying the same Homebrew software, and the same
versions of it — without either machine being able to silently uninstall
something on the other.

The sixth rig: a single statically-linked Go binary, zero runtime deps,
installable by `curl | sh` / Homebrew on any machine Homebrew runs on.

```sh
brewrig init                 # wizard: name this machine, pick a PRIVATE repo, first snapshot
brewrig status               # what's missing here, what's outdated, where versions differ
brewrig apply                # install what's missing (asks, one at a time, before any removal)
brewrig apply -n             # print the plan and stop
brewrig sync                 # publish this machine's inventory
brewrig update               # brew update + upgrade, then publish the new versions
brewrig skip <pkg>           # this machine deliberately doesn't want it
brewrig doctor               # health-check the setup
brewrig ui                   # interactive dashboard (bare `brewrig` lands here)
```

## What makes it not just a shared Brewfile

`brew bundle dump` into a synced folder is the obvious answer, and it breaks in
three specific ways:

- **A single Brewfile is last-writer-wins.** Whichever Mac dumped most recently
  is "the truth", and the other's ad-hoc installs are invisible until overwritten.
- **`brew bundle --cleanup` is the only removal story, and it is all-or-nothing** —
  it uninstalls everything not in the file.
- **A Brewfile cannot express intent.** It cannot tell "machine B hasn't installed
  this yet" from "machine B deliberately removed it". Those need opposite
  responses.

So brewrig syncs no Brewfile. Each machine publishes its own inventory to
`machines/<name>.json` in your private repo, and everything else is derived.
A machine only ever writes its own file, so two machines syncing at once touch
disjoint paths.

## The rules it plays by

- **Only what you asked for is published**, never the dependency closure — the
  signal is `installed_on_request`, not `brew leaves`. (On the machine this was
  built against that is 41 packages out of 143 installed, and `leaves` would have
  wrongly dropped two of them.)
- **Additive by default.** A package only one machine has is not drift; it is
  something the other hasn't caught up on.
- **One removal case, always confirmed.** A package you deliberately uninstalled
  is recorded as `retired` and offered on the other machine — one prompt per
  package, never in a non-interactive run. Say no and it is remembered, so you
  are not asked again.
- **A reinstall beats a retirement**, by timestamp. Without that the two machines
  deadlock: A uninstalls, B reinstalls, A uninstalls, forever.
- **Versions are reported separately from packages.** `missing` → `apply`;
  `outdated` → `update`; `skew` (the machines on different versions of the same
  thing) → `update` on whichever is behind. Skew is the one a Brewfile cannot
  show you at all.

## Not Windows

Homebrew doesn't run there, so brewrig ships darwin and linux only. It is the
first rig that isn't on all three, and that is a property of Homebrew rather than
a gap to fill.

Design notes: [docs/BREWRIG-DESIGN.md](../../docs/BREWRIG-DESIGN.md).
