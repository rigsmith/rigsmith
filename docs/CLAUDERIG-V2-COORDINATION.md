# V2 staging-store coordination

Milestone 6a adds `internal/agentrig/storelock`. Claude is its first consumer;
Codex can use the same mechanism with its own staging directory and wait policy.
This is a behavior change on `codex/v2`. It adds no queue, worker, configuration
key, hook installation change, or backup-format migration.

## Ownership and identity

The canonical staging directory identifies a store. Existing directory symlinks
are resolved; for an absent clone destination, its parent is created and resolved
without creating the destination itself. The lock is the sibling
`.<store-name>.agentrig.lock`, so parent/root symlink aliases converge and
case aliases on a case-folding filesystem address the same lock file. Different
stores have independent locks. Callers must supply the staging root, not a child
directory or remote URL.

Linux/macOS use nonblocking `flock`; Windows uses nonblocking exclusive
`LockFileEx`. Ownership belongs to an open file handle, not a PID, timestamp,
file's age, or the presence of a token. Normal release closes the handle; process
death releases ownership without stale-file deletion. Live ownership never
expires. The lock file persists outside Git and must never be removed as stale:
unlinking/replacing it could give a waiter and a new caller different inodes.

The supported layout is a stable local filesystem staging directory. Do not
rename the store, retarget its symlinks, replace its lock, or mount a different
filesystem over its parent during operations. Network filesystems and shared
multi-host staging directories are outside this contract. Process death does not
roll back partial writes or terminate orphaned Git/mergetool children; abandoned
merge repair remains necessary. Worker lifecycle must handle child-process
ownership before queued hooks are offered. Different clones still
reconcile through Git; this is not remote/distributed ownership. Read-only status
and search remain unlocked and can observe an operation in progress.

## Operation context

`Acquire(ctx, staging, wait)` returns an operation context and an idempotent
release function. The context is a capability for **sequential** nested calls;
it must not be passed to independent concurrent writers. Nested acquisitions
borrow the same OS lease using file identity, and the final release closes it.
An already released lease cannot bypass a new owner's lock. Nesting a different
store is rejected to avoid lock-order deadlocks. Independent operations start
with independent contexts.

Cancellation interrupts acquisition and returns the caller's context error.
Zero wait tries once. Positive wait limits contention and returns `ErrBusy` if
the budget expires; it does not limit the operation's lifetime after acquisition.
Once work starts, ownership stays held until the operation returns, including
when cancellation is waiting for native synchronous filesystem work to finish.

## Claude boundaries

| Boundary | Scope and contention |
| --- | --- |
| CLI sync | Owns the store before reading chunk mode/debounce state, then through repair, capture, journalling and publication. Ordinary/noninteractive hooks skip busy stores. Interactive sync and flush wait up to 15 seconds. |
| Service sync | Acquires or borrows ownership through all phases and failure journalling; independent callers wait up to 15 seconds. |
| Service pull / SessionStart | Tries once before clone, merge repair, reconciliation, auto-restore and its journal. Busy/canceled acquisition is exposed as `CoordinationError`; the hook reports it and retains its best-effort successful exit. |
| Restore command | Holds ownership across staging pull/clone, manifest/preview, confirmation, target backup, restore and journal. Waits up to 15 seconds. |
| Publication, reconcile, repair and finish-merge services | Acquire for independent calls or borrow the caller's operation lease. Repair cannot declare a busy store safe. |
| Merge/abort, repo gc/prune, ledger backfill, device removal | Hold ownership around the full command read/decide/write sequence, including confirmation. Wait up to 15 seconds. |
| Doctor pending-merge fix | Acquires before the repair, rechecks whether a merge remains, then holds through resolution/commit. |

Native credential/profile locks remain separate. Store ownership cannot stop
Claude itself writing live transcripts or changing accounts. Low-level engine,
Git, manifest, journal, registry, ledger and session-deletion helpers remain
caller-owned primitives; they do not independently infer a store from filenames.
Current production staging writers enter through the boundaries above. A future
UI deletion path or worker must acquire this lease around its whole operation.
Shared publication also retains its explicit caller-ownership contract.

The CLI retains `.sync.lock` as a second, legacy guard for older v1 **sync**
clients, including existing token parsing and stale recovery. New store ownership
is acquired first, so aging/replacing the legacy token cannot admit overlapping
v2 writers. The two acquisition waits share the same budget. This bridge does
not make v1 pull/restore/maintenance, raw Git or older services participate; use
one v2 version for all operations sharing a local store. Removing the legacy
bridge is a later upgrade-policy decision.

## Validation and next work

Tests cover independent processes, process death, persistent lock reuse,
same-process contention, bounded wait/cancellation, old live owners, canonical
parent/root aliases, case aliases where supported, absent clone destinations,
separate stores and nested/released contexts. Service and command tests require
contention to stop work before capture/clone/restore/journalling. A gated binary
test verifies busy hooks skip, then sync and restore converge after release.
The unchanged six-scenario compatibility baseline remains pinned at `d39a446`.

Next, milestone 6b adds durable jobs and worker ownership on top of these
operation boundaries: generation-aware acknowledgements, flush coalescing,
offline/retry recovery, captured provenance, and queue upgrade policy. Installed
hooks stay synchronous until the later opt-in rollout milestone.
