# Shared session records and Git publication

Milestone 5 adds `internal/agentrig/records` and
`internal/agentrig/publication`. Claude's existing services, command output,
backup paths and persisted schemas remain authoritative. This is an internal
extraction, with no queue, new executable, setting or storage migration.

## Session records

`records.Summary` is an in-memory view of a native session: opaque vendor and
session IDs, title, project/cwd, activity with an approximation flag, size,
recording provenance and optional account/profile facts. Query consumers can
also use last prompt, branch and client. A store handles one vendor's ID
namespace. Shared code does not parse UUIDs, infer ownership, rank attribution
sources, or combine independent vendor stores.

`records.Record` owns the visit/refresh/read/note/save loop. Readers provide cheap
candidate facts, request explicit refresh or revocation, and defer expensive
reads until a record needs them. The native store supplies freshness checks,
attribution merging, row persistence and counts. Enumeration or hydration errors
return without saving the partial walk. Candidate order is preserved.

Claude's engine still selects top-level staged CLI transcripts, reads native
activity and prompts, handles chunks, and selects attribution from Desktop
sidecars or the syncing machine's eligible sessions. It compares evidence
against the cross-device ledger before requesting refresh. Contested attribution
is revoked without recording another guess. The adapter translates the resulting
summary to `ledger.Entry`; `Ledger.Note` and the existing JSONL serializer retain
unknown fields, the original attribution timestamp and all legacy merge rules.
No vendor/profile field is added to those rows.

`LedgerSummary` and `MetadataSummary` expose native ledger and Desktop facts to
queries. The shared query functions preserve literal title matching and the
session list's existing visible-field filter. An empty title query is rejected;
a blank list filter still includes all rows. They do not broaden title search
to cwd, attribution or transcript bodies. Native content scanning, source
aggregation, account resolution, resume hints, deletion and Desktop operations
remain in Claude packages. The shared summary is not serialized into CLI JSON
responses; existing output types still determine that format.

## Publication

`publication.Workflow` runs commit, push/reconcile and history maintenance using
`core/gitrepo`. Required caller policies supply repository initialization, byte
preparation, validation, the publication audit, conflict handling and the
human-resolution error. Missing policies and incomplete plans fail before initialization. History plans
require a distinct branch, explicit nonempty pathspecs and messages, and a positive
commit limit. Retention requires positive resolved keep-days, a fold label, and
nonnegative finite thresholds (zero thresholds remain valid). Claude's
service translates shared progress back to its existing event types, including
native resolution details from the conflict callback.

Initialization is explicitly injected: the existing `gitrepo.Init` chooses a
Claude fallback Git identity. Claude keeps using it, while another vendor can
supply its own initialization without inheriting that identity. Existing core
Git primitives are unchanged.

The Claude adapter supplies the publication plan:

- `origin` and `main`, existing commit labels, and three reconciliations after an
  initial failed push (up to four push attempts).
- `config-history` with pathspecs `.` and `:!cli/projects`. Desktop/profile
  sessions remain in this history branch; this extraction does not redefine
  what counts as configuration.
- The existing 200-commit history limit, retention floor/factor, keep-days
  calculation and history-folding commit label.

The workflow preserves failure ordering: prepare and audit before committing;
validate and audit before each push; apply native conflict policies; reject
unstaged merge resolutions; prepare and audit before committing a merge. An
audit-refused merge remains pending. A failed push leaves the local commit for
later retry even when a new capture produces no changes. Local-only publication
returns before history maintenance, as it did previously.

Publication success is reported before maintenance. The selected history branch
is maintained best-effort. Size maintenance repacks and remeasures first, then
folds history at local midnight and force-pushes only if needed. It retains the
existing error handling and force-push policy; coordination and competing-writer
protection are milestone 6, not implicit guarantees of this extraction.

Device, manifest, ledger and journal serializers remain native. The calling
Claude sync service records capture/device metadata before publication and
retains journal ownership. Pull's fresh-machine auto-restore and operation locks
also stay at the existing Claude boundaries.

## Validation and next step

Vendor-neutral tests exercise offline publication/retry, arbitrary remote and
history selection, mandatory audits, and refusing merged content before commit.
Record tests cover opaque IDs, fresh-read avoidance, policy refresh, revocation,
partial-walk failures and query scope. Adapter tests compare the exact ledger
bytes written through the shared summary with native writes, including unknown
fields and stronger existing attribution. Claude's publication test explicitly
keeps Desktop sessions in config history while excluding CLI transcripts.

The full synthetic suite and the unchanged pinned compatibility baseline remain
the Linux/macOS/Windows gate. After review and merge, milestone 6 establishes
store-wide coordination before introducing durable queued work. The separately
documented restore/prune edge case remains a separate fix.
