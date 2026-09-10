# Explicit queued Claude commands (7b)

V2 exposes a foreground queue workflow for deliberate testing and use. It does
not install a worker or route hooks. Ordinary `sync`, `pull` and hooks keep their
existing synchronous behavior. Use v2 clients for operations sharing staging.
Actual OS reboot/hibernation validation remains a general-release gate.

Bare `queue` on a terminal offers init, status, run and drain. Prepare/enqueue/retry
require explicit command arguments; off a terminal, bare `queue` prints help.

## Start and accept work

Configure the private remote, then run an ordinary `clauderig sync` to initialize
local and remote history. For an existing GitLab repo or token-only setup, use
`clauderig config set remote https://gitlab.com/acme/private-backup.git` (substitute
your provider/URL), or noninteractive `clauderig init --yes --remote <url>`. These
paths invoke the provider-aware private-repo verifier. The existing interactive
init wizard still skips remote setup without `gh`, and doctor may report privacy
unverified without `gh`; this queue preview does not change those older gates.
Queue init/run/drain perform their own provider-aware checks.
The queue uses the configured private HTTPS GitHub/GitLab remote and existing
Git credential helpers (`gh auth setup-git` where appropriate). Privacy checks use
`gh` for GitHub and `glab` for GitLab, falling back to `GITHUB_TOKEN`/`GH_TOKEN`
or `GITLAB_TOKEN`/`GL_TOKEN` when the matching CLI is absent. It adds no authentication store or
SSH transport. Existing SSH users continue with ordinary sync.

```sh
clauderig queue init
clauderig queue prepare --session my-session --output request.json --flush
clauderig queue enqueue request.json
clauderig queue status
clauderig queue drain
```

Choose a private directory for request files, outside synced source/staging and
the managed runtime tree. Both prepare and enqueue reject these trees after
canonicalizing existing ancestors, including symlink aliases. The entire Desktop
profile store and discovered profile/data-directory targets are excluded, including
profiles not selected for the queue and separately symlinked data directories. `prepare` creates a new file with mode 0600, refuses an
existing file and flushes the saved request before succeeding. Keep Windows
runtime/request files under a private user directory: inherited ACLs are a caller
prerequisite, not validated or repaired by these commands. Linux/macOS runtime
reopening checks effective ownership and private directory/descriptor modes.

`prepare` reads account identity once, generates one event ID and timestamp, and
pins the runtime binding. Session IDs are trimmed and lowercased to match native
transcript lookup, then checked against the identifier size bound. Prepare and
enqueue require one literal session name: separators, `.`/`..` and glob patterns
are refused using the native session mover restrictions. Saved session IDs must
already be trimmed and lowercase; admission refuses rather than rewriting them. It saves intent and validated attribution, not transcript
bytes. `--session` is required; `--flush` captures every changed transcript tail.
Without it, normal capture policy applies. Valid email/organization observations
are retained even without an account UUID. New prepared requests trim and
lowercase both UUID fields before hashing and saving; invalid UUIDs refuse
preparation. Previously saved requests and retained provenance hashes are not
rewritten or migrated. Admission requires saved nonempty UUID fields to already
be canonical; admission rejects noncanonical documents without modifying them. A completely empty identity requires the explicit
`--unknown-identity` flag. This flag bypasses live identity lookup and records
unknown attribution. No worker reads its current account to attribute saved work.

**Retry `enqueue` with the same saved file.** Never recreate a request after an
uncertain result, change its timestamp or substitute a later account. `enqueue`
serializes access with a sibling request lock, rejects changed file identity or
contents, and reflushes exactly the validated bytes before accepting the request.
Keep the sibling lock file in place while producers may be running. A successful response
reports its `generation` and `batch`, including on duplicate receipt retries. The
file must remain stable while the command runs. Retain it until acceptance is
confirmed; keeping it through completion makes a lost CLI response retryable.
Request link counts are checked through open file handles on Linux/macOS/Windows
both on load and before confirmation. Existing multiply linked outputs are never
overwritten; remove aliases from synced trees before retrying. Request inputs
are regular files, bounded to 128 KiB, with a strict versioned JSON
shape and a checksum to detect accidental corruption (not authentication). Symbolic or multiple-hard-linked files, extra JSON, duplicate/case-aliased or
unknown fields, invalid attribution and a foreign
runtime binding refuse admission. Failed preparation may leave a partial or
complete file; inspect it instead of overwriting it. Do not edit saved requests.

Runtime creation and reopening enforce the same exclusion of all Desktop
profile and data-directory targets, even when no profiles are selected. A runtime
that becomes exposed through a new profile link refuses reopening without changing
its saved state. Keep filesystem roots and profile links stable during operations.
Unresolved links in enabled source roots, staging, runtime paths, or at the
Desktop store, profile or data-directory level refuse
queue operations before creating a runtime or its target directory. An unreadable
or invalid profile store also refuses reopening with a queue-isolation diagnostic.
Repair the local path/permissions and retry using the same runtime; do not reset
its descriptor, copy its children or bypass the check. This conservative refusal
also applies to status, because reopening cannot establish the isolation boundary.
Windows resolves existing paths through an opened handle, including directory
junctions; unresolved junctions refuse before missing suffix directories are created.
Volumes without DOS drive names retain a rooted volume-GUID path. Keep volume
mappings stable for the lifetime of a runtime.

The default runtime is `~/.clauderig/queue-runtime`. Use `--dir` to select another
private, stable root outside sources and staging. The managed stores belong
exclusively to this lifecycle. Never copy/reset individual children or point
independent runtimes at those stores. See [runtime contract](CLAUDERIG-V2-QUEUE-RUNTIME.md).

Desktop profiles are explicit: repeat `--profile <name>` on **every** command for
the same runtime. No profiles means none; workers do not discover new profiles.
Changing profiles, source roots, machine or capture configuration refuses old
work rather than redirecting it. Reopening saved phases retains the original
chunk mode even after loss of the live staging marker; fresh captures still
validate the live mode.

## Saved request format (version 1)

Only `prepare` writes new request documents. It emits every field below, including
empty identity strings and `Flush.Paths: null`. Enqueue checks the exact member
spelling and types before decoding; unknown, duplicate or case-aliased fields,
invalid UTF-8, missing required members and extra JSON objects are refused.
No missing identity or timestamp is filled from the current account or clock.

| Member | JSON type | Writer / purpose |
| --- | --- | --- |
| `Checksum` | string, required | Prepare: lowercase SHA-256 described below; detects corruption, not authentication. |
| `Version` | integer, required | Prepare: exactly `1`; other versions refuse. |
| `Scope` | string, required | Prepare: runtime ScopeID digest, checked against the opened lifecycle before admission. |
| `At` | timestamp string, required | Prepare: original UTC time in Go's RFC3339Nano representation; must be nonzero and retained across retries. |
| `Identity` | object, required | Prepare: the single producer observation; null or omission is invalid. |
| `Identity.AccountUUID` | string, required | Canonical observed account UUID or empty. |
| `Identity.OrganizationUUID` | string, required | Canonical observed organization UUID or empty. |
| `Identity.Email` | string, required | Observed validated email or empty. |
| `Request` | object, required | Prepare: immutable intent; null or omission is invalid. |
| `Request.EventID` | string, required | Prepare: new random event identifier; retained across retries. |
| `Request.SessionID` | string, required | Prepare: trimmed, lowercase `--session` value. |
| `Request.ProvenanceID` | string, required | Prepare: `service.CaptureProvenance(Identity)`; enqueue recomputes and checks it. |
| `Request.Flush` | object, required | Prepare: selected capture intent; null or omission is invalid. |
| `Request.Flush.Mode` | string, required | Prepare: `normal` by default, `all` with `--flush`. |
| `Request.Flush.Paths` | array of strings or null, optional | Prepare emits null. Omission and null decode to no selected paths. Only `selected` mode accepts a nonempty path list. |

The reader supports the shared `selected` flush mode with a nonempty list of
bounded paths, but this manual CLI produces only `normal` and `all`. Normal/all
require no paths; omission does not default `Mode`. Queue admission also enforces
bounded event/session/provenance identifiers and the existing 64 KiB intent limit.
The entire producer document is limited to 128 KiB.

For the checksum, decode the exact typed `queueSubmission` representation, set
its `Checksum` to the empty string, and SHA-256 its compact Go `encoding/json`
serialization. Field order is the table order (including nested struct order);
null/omitted `Paths` is represented as null. String escaping and timestamp
serialization follow Go `encoding/json` and `time.Time.MarshalJSON`. Whitespace
and object member order in the input document do not affect this digest.
The supported way to generate a request is `prepare`; do not hand-edit documents
or compute a new checksum to replay an old event with changed intent.

A prepared unknown-identity, non-flush request has this shape (digest, random ID
and timestamp values below are explanatory placeholders, not an enqueueable file):

```json
{
  "Checksum": "<lowercase SHA-256>",
  "Version": 1,
  "Scope": "<runtime ScopeID>",
  "At": "2026-09-10T12:00:00Z",
  "Identity": {"AccountUUID": "", "OrganizationUUID": "", "Email": ""},
  "Request": {
    "EventID": "<new random ID>",
    "SessionID": "abcdefab-1234-4123-8123-abcdefabcdef",
    "ProvenanceID": "<CaptureProvenance of the empty identity>",
    "Flush": {"Mode": "normal", "Paths": null}
  }
}
```

`--unknown-identity` explicitly creates the empty identity object above without
reading the live account. Without that flag, a wholly empty observation refuses
preparation; a valid partial email/organization observation is preserved in the
request and runtime descriptor. The existing session ledger still records account
UUID attribution only. Missing UUIDs do not become email/organization ledger keys,
and delayed captures do not rewrite the current device registry with old identity.
This command does not change the ledger or device-registry schema.

## Run, stop, retry and drain

`clauderig queue run` stays in the foreground, polls for work and resumes saved
phases. Only one loop owns the runtime. The actual binary selects the audited
Unix supervisor entrypoint or Windows native job ownership for Git writers.
Startup verifies shared history under staging ownership **before** claiming any
work, including for an empty drain. Privacy is verified at startup and again
when resolving each batch. An uninitialized/unrelated history requires explicit
foreground initialization or recovery. Startup failures do not consume attempts.

The first Ctrl-C or SIGTERM stops after the current batch and its child cleanup.
A second signal cancels the active operation and still waits for supervised
cleanup. A stopped worker does not imply an empty queue. Persistent process
fences remain when cleanup cannot be confirmed; `retry` does not clear them.
[Process lifecycle requirements](CLAUDERIG-V2-PROCESS-LIFECYCLE.md) describe recovery.

`queue status` prints lower-camel-case JSON (`batches` and `capacity`), with batch IDs, phases, status, event counts, attempts,
retry deadlines, failure codes and queue capacity. It omits account details,
transcript paths and raw errors. Batch and capacity reads are separate snapshots;
active producers/workers can change them between reads. Capacity describes queue
metadata, not total disk usage. Worker diagnostics go to stderr.

After fixing a blocked batch's reported problem, run `queue retry <batch-id>`.
This explicitly unblocks it, preserving attempts and saved captures/commits.
Timed pending retries keep their backoff. Unknown IDs and invalid transitions
fail. The command does not delete accepted work or bypass configuration checks.

Stop producers, then run `queue drain` to process ready work until idle. Delayed
or blocked work returns a nonzero result and remains saved. Drain does not wait
through backoff or silently unblock failures. An interrupted drain also reports
failure; inspect status and drain again. Concurrent producers can extend a drain
or arrive after its final empty observation, so completion requires stopping
producers externally. No hook producer is wired in this slice.

Both worker commands accept `--max-archive-bytes` (0: existing 32 GiB default)
and `--max-stored-bytes` (0: unlimited direct sealed bytes per store). These are
admission limits, not total/peak disk reservations; scratch and recovery substores
are excluded. Raising a limit can allow retained work to proceed after an explicit
retry. This slice exposes no archive deletion or receipt compaction command.

## Next

Milestone 7c.1 adds the internal runtime manual-sync coverage bridge.
Milestone 7c.2 connects commands, opt-in hooks and stop/drain/rollback.
Until then, explicit queue work and synchronous sync can serialize on staging,
but a synchronous sync does not acknowledge pending queue events. Request-file
preparation is the manual producer contract, not the future hook input protocol.
