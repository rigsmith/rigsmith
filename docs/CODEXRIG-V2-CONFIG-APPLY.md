# Codex config file application (8b.5)

`ConfigRestorePlan.Apply` now consumes a prepared plan and installs its changed
base/profile files. This is an internal API, not a new command. A production
caller must still provide the supported-version validator required by
[preparation](CODEXRIG-V2-CONFIG-RESTORE.md), coordinate Codex and other editors,
and connect independent CodexRig state and user-facing restore policy.

## Applying a plan

- A no-op plan checks the destination and closes without creating a lock or
  scratch file. Otherwise application acquires the shared destination writer
  lock and checks the entire original configuration again.
- Every changed file is staged before any destination file is installed. Staging
  checks original content, records file identity/mode/mtime, writes a random
  temporary sibling, flushes its data, and closes it. A failed stage cannot be
  applied. Existing config files are never truncated during staging.
- Before each installation, the adapter checks the entire selected config set.
  The shared writer checks its lock identity, destination identity/metadata/hash,
  and staged identity/hash. Same-size changes with restored timestamps are
  detected by content fingerprints. Checks after each successful installation
  account for this operation's own writes while protecting retained profiles.
- Existing files are installed with root-relative rename. New files are installed
  with no-replace hard-link creation, so a newly arrived destination cannot be
  overwritten. Unsupported hard-link filesystems refuse creation; there is no
  remove/copy/truncate fallback. Temporary links are then removed.
- The writer checks the installed object's identity and contents and the pinned
  root. Unix directory entries are synced. Windows file data is flushed before
  installation, but this API does not provide a directory-fsync or universal
  power-loss guarantee. The standard library's Windows rename contract also does
  not promise atomicity. See the [Go filesystem API](https://pkg.go.dev/os@go1.26.7#Rename).

Application always closes the plan, including on contention, cancellation or
refusal. A consumed plan cannot be applied again. Unchanged and omitted files
retain their exact bytes. New/replaced files use the temporary file's 0600 mode
on Unix; Windows uses inherited directory ACLs. Existing file ACLs, ownership,
extended attributes and hard-link relationships are not copied to replacements.
No live config or credentials are used by the tests.

## Shared writer boundary

`internal/agentrig/files.BeginReplace` owns the reusable replacement mechanics.
It requires an already pinned `Source` and uses its root-relative filesystem
operations. It does not interpret Codex files or import a vendor adapter. Claude's
existing file-writing helpers and callers are unchanged.

The fixed `.agentrig-replace.lock` is an empty regular direct child. The writer
creates it exclusively or opens an existing regular object without following
links, then takes a nonblocking OS lock (`flock` or `LockFileEx`). It checks that
the name still refers to the locked object. Closing releases the OS lock; the
empty file stays in place to avoid splitting ownership across different inodes.
A competing participating writer gets `ErrReplacementBusy`.
Config enumeration budgets the persistent lock and staged siblings separately
from the 4,096-user-entry bound: at most 33 reserved artifact names and 4,129
total names. Preparation includes incoming new files in the user-entry budget,
so our own lock, staging and creation cannot consume unreserved capacity.
Old scratch names count toward the same bounded artifact allowance. After
acquiring the lock, application reserves room for every changed file before
staging any private config bytes. If leftovers leave insufficient room, it
refuses the batch without staging or changing targets. Recognizing a reserved
name never establishes ownership or permits cleanup.
Replacement targets cannot use the lock name or scratch prefix in any letter
case, so case-insensitive filesystems cannot alias these reserved objects.

Random `.agentrig-replace-*` files contain private staged output and are excluded
from Codex capture by the existing file policy. Normal cleanup removes only
scratch names still referring to this batch's regular objects. Foreign/replaced
scratch is left alone and reported with `ErrReplacementCleanup`. Cleanup never
removes target files or the fixed lock. Old crash leftovers are not automatically
deleted or interpreted as new input.

## Failure and concurrency contract

`ConfigRestoreResult.Applied` contains the relative names of confirmed completed
replacements. `Uncertain` identifies the current file when an installation or
post-installation check may have completed but cannot be confirmed. The error is
identifiable as `files.ErrReplacementUncertain`; diagnostics never repeat private
content or filesystem error text. Cleanup failures remain identifiable too.

A pre-installation refusal leaves that target intact. Earlier confirmed files
remain installed if a later file fails. There is no automatic rollback, retry,
or multi-file transaction. After an error, inspect the result and destination and
prepare a fresh plan. A process crash can leave a partially applied set and private
scratch; crash recovery/cleanup integration remains a user-facing workflow gate.

The OS lock coordinates participating restore writers, not Codex, older clients,
or arbitrary filesystem writers. Codex and other editors must be idle during
application. Identity and hash checks detect observed changes; they cannot make
check-plus-replace atomic against a nonparticipating writer or defend every
mutation by an actor controlling the directory. Cancellation after installation
starts is treated as uncertain rather than falsely reported as an untouched file.

Synthetic tests cover complete round trips, repeated no-ops, local secrets,
staging failure, stale plans, lock contention and release, destination/staged-file
changes, link refusal, foreign-scratch preservation, cancellation, partial and
uncertain reports, and changes to retained profiles between installations.
Native CI exercises Linux, macOS and Windows with the Claude compatibility suite.
