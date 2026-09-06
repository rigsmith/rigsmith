# clauderig UI — roadmap to the split

*Written 2026-09-05, after a day that turned up two data-loss bugs in the sync engine
and one in how sessions are found. Sequenced around a constraint discovered while
planning it, not assumed: see [The split's real cost](#the-splits-real-cost).*

Goal, in the order asked for:

1. Surface `clauderig doctor` in the window as another pane
2. A full sessions browser in the window, tied to the filing checks, so a user can
   maintain their own history
3. Move the UI into its own repository
4. **First**, land the CLI-side work all of that needs, so rigsmith can ship

## The split's real cost

`ui/` imports thirteen `internal/clauderig/*` packages. Go's `internal` rule is a
**path prefix** check, so anything under `github.com/rigsmith/rigsmith/` may import them
and anything else may not. A separate repository is "anything else".

Two facts settle the shape of this, both measured rather than assumed:

- A **submodule** at `ui/go.mod` *can* import the parent's internal packages — the prefix
  still matches. Verified by building one. It would give independent versioning for free.
  Ruled out: nested modules are a standing tax on tooling, editors and CI.
- A **separate repository** cannot, at any price. The packages it needs must become public
  API of the rigsmith module first, and public API is a promise that outlives the reason
  for making it.

So the split is not a repository operation. It is an API design task, and it belongs in
the release **before** the move — which is exactly why (4) comes first.

The UI needs one symbol from `internal/clauderig/commands` (`SquashedRoot`) and pulls in
the whole cobra tree to get it. That one is a mistake to fix regardless of the split.

## Phase 1 — the release (CLI + shared)

Everything here ships in the next rigsmith version. Nothing here depends on the UI.

**1.1 — What is already on the branch.** 50 commits, 21 changesets. Two shipped data-loss
bugs fixed (the automatic squash measuring unpacked objects and discarding history it did
not need to; hook syncs with no debounce or lock), three counters that reported properties
of the tree as properties of a run, and the session-filing checks. This is the bulk of the
release and it is done.

**1.2 — A public surface for the UI.** *Dropped, 2026-09-05.* Public API is a maintenance
promise that outlives the reason for making it, and the only reason here was a repository
move nobody has committed to. The UI stays in this repository, on one module, importing
`internal/` as it does today. Nothing else in this roadmap depends on the decision, so it
can be taken later — with the difference that Phase 2 will have shown whether the UI's
release cadence actually diverges from rigsmith's, which is the only fact that would
justify the promise.

**1.3 — Doctor, callable rather than printable.** `doctor.Run` returns `[]Section` whose
`Result.Fix` is a `func(context.Context) error`. A function cannot cross the bridge, so a
pane cannot offer the fixes the CLI offers. Give each check a stable id, return that, and
add a "run the fix for id" call. The CLI keeps its current behaviour; the id is what makes
the same repairs reachable from anywhere else.

**1.4 — `SquashedRoot` out of `commands`.** One symbol, and it drags cobra into the UI
binary. Move it to where the knowledge belongs (`gitrepo` knows the shape; clauderig knows
which messages are its own).

## Phase 2 — the two panes

Built in this repository against the Phase 1 surface, because iterating is cheaper here
than across a module boundary.

**2.1 — Doctor pane.** The checks, their status, and a button per fixable one. The pane is
worth having the moment `doctor` grows a check the CLI would print and nobody would read —
which already happened today with session filing.

**2.2 — Sessions browser.** The full window already lists, searches, opens and deletes.
What it does not do is tie those to the filing checks: a session filed in two places should
say so *in the row*, and the drawer should offer the consolidation the status pane offers.
The maintenance story is the point — a user should be able to find and fix their own split
sessions without knowing the words "split session".

## Phase 3 — the move

*Not scheduled.* Kept here as the record of what it would cost, not as a plan. Revisit only
if the UI's releases start wanting a cadence of their own; until then one repository and one
module is strictly less work, and the API promise above is the price of changing that.

If it ever happens:

1. New repository, `require github.com/rigsmith/rigsmith vX.Y.Z` against a released tag,
   which requires 1.2 above.
2. Move `ui/`, its packaging script, its cask publisher, and the macOS job.
3. Delete `ui/` here; the `clauderig-ui` build and archive leave `.goreleaser.yaml` with it.
4. Its own release lane, its own version.

The macOS packaging already runs as a separate job for its own reasons — cgo, a bundle,
`codesign` rather than quill — so it moves as a unit rather than being untangled.

## What this sequence buys

The release is not blocked on the UI. The UI's dependencies become explicit before they
become a boundary, rather than being discovered as one during the move. And the API is
designed once, deliberately, instead of being whatever thirteen imports happened to need
on the day someone tried to split the repository.
