# brewRig commands

## `brewrig init`

Names this machine, points it at a private git repo shared with your other
machines, and publishes its first inventory.

The remote **must be private** — verified through `gh`, not taken on trust. A
package list is a decent map of what you work on.

| Flag | |
|---|---|
| `--remote <url>` | private repo URL, verified via `gh` |
| `--machine <name>` | name for this machine (default: its hostname, cleaned up) |

With both flags it runs unattended; otherwise it asks for whatever is missing.

## `brewrig status`

Three separate reports — missing here, outdated here, and version skew between
machines — plus which machines have published and when. `--offline` reports
against the last pull without contacting the remote.

## `brewrig apply`

Installs everything the other machines have that this one doesn't, tapping any
third-party taps first.

It will not uninstall anything to make the machines match. The one exception is
a package deliberately removed elsewhere, which is offered **one prompt at a
time**, naming the machine that removed it and when — and only on a terminal. A
non-interactive run says how many are pending and changes nothing.

| Flag | |
|---|---|
| `-n`, `--dry-run` | print the plan and stop |
| `--yes` | skip the confirmation before installing (removals still ask) |

A package already present as a *dependency* is listed too, marked
`already here as a dependency`: installing it is a near-instant no-op that
records you meant to have it.

## `brewrig sync`

Publishes this machine's inventory and reports how the machines now differ.
Publishing only — nothing is installed unless you pass `--apply`.

A sync that changed nothing makes no commit, so two machines don't publish
reordered-but-identical files at each other.

## `brewrig update`

`brew update`, then `brew upgrade`, then publishes the resulting versions. This
is the fix for version skew: run it on whichever machine `status` reports as
behind. brewRig moves machines forward, never back — it does not pin or
downgrade.

`--greedy` also upgrades casks that auto-update themselves. Off by default
because upgrading one can restart a running app.

## `brewrig skip <package>`

Records that this machine deliberately doesn't want a package the others have,
and publishes the decision so it stops being proposed. `--cask` for a cask.

Undo it by installing the package normally.

## Two runs at once

Two brewrig processes on one machine share a clone, so they are serialised.

If a run loses that race it stops with *"the published inventory changed while
this run was preparing its own; run brewrig again"* and changes nothing. Running
it again is the fix — the next run reads the new state and proceeds. It is
deliberately not automatic: the alternative to refusing is silently replacing
the other run's work, and a "keep" decision is exactly the kind of thing that
would vanish.

Waiting for the other run gives up after five seconds and names the lock
(`machines/.lock` inside the staging clone). A lock left behind by a crash is
taken over automatically after two minutes; there is nothing to clean up by
hand.

Two separate *machines* never hit any of this — they write different files, and
git settles the push.

## `brewrig doctor`

Checks Homebrew, git, `gh`, the config, the remote's reachability, and how many
machines have published.

## `brewrig ui`

The interactive dashboard, which a bare `brewrig` on a terminal lands on. Leads
with the current situation, then the verbs.

## Configuration

`~/.brewrig/config.json` (override the directory with `BREWRIG_HOME`):

| Key | |
|---|---|
| `remote` | the private repo holding `machines/*.json` |
| `machine` | this machine's name |
| `branch` | branch published to (default `main`) |
| `greedyCasks` | make `--greedy` the default for `update` |
| `autoApply` | let `sync` install what's missing too (removals still never automatic) |
