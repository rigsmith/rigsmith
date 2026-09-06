# Shared file mechanics

Milestone 4 moves existing filesystem mechanics into `internal/agentrig`.
ClaudeRig calls them through its existing engine and allowlist entry points.
Both shared packages depend only on Go's standard library. They introduce no
command, setting, backup migration, queue, or Codex runtime dependency.

## Ownership

| Shared mechanism | Caller-supplied Claude policy |
| --- | --- |
| `allowlist.List`, `Match`, `Descend`, `Walk` | Existing CLI, Desktop and profile rules in `internal/clauderig/allowlist/defaults.go`; selected roots from the adapter. |
| `files.Write`, `Copy` | Bytes or source paths, destination paths, and creation permissions. |
| `files.WriteMtime`, `CopySnapshot` | Already-scanned bytes or a source selected for snapshotting, and source mtime. |
| `files.Reconcile`, `RemoveEmptyDirs` | A predicate deciding which staged paths remain allowed. |
| `files.Restore` | Destination mapping, storage-part exclusions, live-file set, and a required write/codec callback. |
| `files.Prune` | Explicit authoritative directories and the restore result's retained/protected paths. |
| `files.LinkCache` | One target root per cache, also used by Claude's manifest-link restoration. |

The matcher retains default deny, specificity and tie ordering, hard any-depth
prunes, sorted results, and directory-link containment rules. The Claude facade
aliases shared types and supplies unchanged defaults. Existing policy tests stay
with Claude; shared tests use arbitrary rules and vendor-neutral trees.
A review follow-up corrects one inherited containment bug: allowed directory-link
targets named `..named` remain inside the root. Parent traversal (`..` or a path
beneath it) and links to the root itself remain excluded.

Claude still owns the sync per-file policy loop: retention, throttling, flush
selection, redaction, scanning, portable JSON transforms and chunk decisions.
Snapshot helpers do not infer a codec from a filename. Claude's restore callback
selects its existing JSON merge or transcript materializer; raw fallback remains
a Claude choice. A later TOML codec can use the same callback without teaching
shared code about TOML or inheriting Claude's fallback.

## Compatibility details

`CopySnapshot` writes a temporary sibling, finishes copying and setting mtime,
then renames it over the destination. Failure preserves the prior snapshot and
removes the temporary file. Its published mode remains the temporary file's
0600 creation mode, with 0755 parent directories. `WriteMtime` retains the direct
0644 write and 0755 parents for bytes already scanned by the caller. General
copy/write helpers accept permissions; Claude selects 0700/0600 for profiles and
0755/0644 for other restored roots. Existing file modes follow the prior write
semantics. No permission normalization is introduced.

Reconciliation judges only the supplied allowlist predicate. It keeps allowed
files absent from this machine, including empty files, and removes empty
subdirectories after the walk. Missing entries and individual deletion failures
retain the prior best-effort behavior; other walk errors stop reconciliation.
It does not infer that a staged file is obsolete because its live counterpart
has become a directory.

Restore enumerates staged paths, lets Claude skip chunk parts and rewrite project
slugs, checks the mapped destination against live files, then applies symlink
and file/directory collision guards before calling the codec. The shared API
rejects nonlocal mapped destinations. A symlink at the target root itself is
allowed; symlinks below it are preserved. A codec error stops the operation.
Claude counts rewritten slugs during mapping and Desktop sidecars after a
successful write. Manifest links are recreated after files and before pruning,
just as before.

Pruning remains a separate operation, called only after successful restoration.
Claude selects `skills`, `commands`, `agents` and `plans`; project history remains
additive. The result tracks symlink skips as retained and refused destinations
as protected subtrees. The mechanics retain existing limitations rather than
adding unrelated fixes:

- Live skips are reported separately, not inserted into the prune sets. Claude's
  live transcripts are outside authoritative prune directories. Another consumer
  must carry live paths into its protected set if it prunes those directories.
- A regular-file ancestor collision protects the requested destination, not the
  ancestor file. Under an authoritative directory that ancestor can still be
  pruned. This pre-existing case needs a separate regression fix before expanding
  prune policy; it is not the desired long-term contract.
- Prune directory paths and roots are trusted caller inputs. Destination guards
  preserve existing filesystem state but do not lock out concurrent filesystem
  changes. `LinkCache` is scoped to one root and one operation.

No metadata readers, serializers, ledger/search orchestration, Git publication,
or account/Desktop operations move into the shared packages in this milestone.
Those boundaries remain in milestone 5 or in their vendor implementations.

## Validation

Shared tests cover arbitrary allowlist rules and directory links, native bytes,
mtime, snapshot failure cleanup, reconciliation, injected codecs and path mapping,
live-file skips, symlink ancestors (including `..`-prefixed names), root aliases,
refused destination subtrees, scoped pruning and codec failures. Existing Claude
engine and policy tests exercise the wiring. Full synthetic end-to-end tests and
the [pinned compatibility baseline](CLAUDERIG-V2-COMPATIBILITY.md) remain required
on Linux, macOS and Windows. The baseline is not changed by this extraction.
