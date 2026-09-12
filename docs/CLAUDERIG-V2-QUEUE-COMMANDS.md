# Explicit queued Claude commands (v2)

V2 exposes a foreground queue workflow and explicit local hook opt-in. It does
not install a worker service. Sync/hooks remain synchronous by default; `pull`
stays synchronous in either mode. Use v2 clients for operations sharing staging.
Actual OS reboot/hibernation validation remains a general-release gate.

When run outside a terminal, bare `queue` prints help; when run in a terminal,
it offers init, status, sync, run and drain. Prepare/enqueue/retry require explicit
command arguments; hook/inbox commands are also invoked directly.

## Start and accept work

Configure the private remote, then run an ordinary `clauderig sync` to initialize
local and remote history. For an existing GitLab repo or token-only setup, use
`clauderig config set remote https://gitlab.com/acme/private-backup.git` (substitute
your provider/URL), or noninteractive `clauderig init --yes --remote <url>`. These
paths invoke the provider-aware private-repo verifier. The existing interactive
init wizard still skips remote setup without `gh`, and doctor may report privacy
unverified without `gh`; this queue preview does not change those older gates.
Queue init/sync/run/drain perform their own provider-aware checks.
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

## Manual queue sync

`clauderig queue sync` drains saved work through the same worker used by `run`
and `drain`, then performs an ordinary manual sync. Every queued batch keeps its
saved attribution, artifacts and retry progress. A blocked or delayed backlog
stops the command before manual capture; inspect `queue status`, repair/retry the
batch or wait for its retry time. It does not wait through backoff.

```sh
clauderig queue sync --dry-run  # stage/scan only; do not drain or publish
clauderig queue sync --flush    # drain saved work, then sync all changed tails
```

Stop producers before draining for a complete backlog drain. Requests arriving
after the drain remain queued even if manual sync copies their files. Only the
worker completes queue requests after confirming publication; manual sync does
not infer completion from matching files or account identity.

Use the same `--dir` and `--profile` selection as init, including every local
Desktop profile. Missing or unreadable profile metadata refuses manual sync.
Keep source/profile locations and private runtime state stable. On Windows,
private inherited ACLs remain a caller prerequisite. See the
[runtime contract](CLAUDERIG-V2-QUEUE-RUNTIME.md#manual-sync-boundary).

Private remote and initialized shared-history checks still run, including for
previews. Queued work uses its saved producer identity; final manual capture reads
the current identity once. `--flush` affects that final capture. No hook payload,
debounce or external merge tool is used. The first interrupt stops after the
current batch or manual sync; a second cancels and waits for supervised cleanup.

The result reports batches completed by the worker. If final manual sync fails,
those completed batches remain completed. A repeat may copy files again but does
not substitute fresh source bytes for a retained batch. Without hook opt-in,
ordinary sync and installed hooks keep their existing synchronous behavior.

## Prepare a request from hook input (7c.2b.1)

`clauderig queue prepare --hook --output request.json < hook.json` translates a
[Claude Code hook payload](https://code.claude.com/docs/en/hooks) into the same
private, immutable producer file used by manual preparation. It saves intent;
it does not enqueue, publish, install hooks or start a worker. Use a fresh output
file for each new hook event, then `clauderig queue enqueue request.json`. Retry
admission with that same file, never by preparing the payload again. Existing
hook installation remains synchronous; automatic installation remains 7c.2b.2b.2 work. The managed hook producer and
inbox recovery are described below.

The input must be a complete JSON object, including EOF, within two seconds and
128 KiB. Empty/blank, malformed, duplicate or case-aliased routing fields,
trailing documents, invalid UTF-8, and unsupported events fail before identity
capture or request creation. Unlike the legacy `sync --flush` reader, an open
pipe after the object still times out. The command accepts native stdin/files
and interruptible pipes; embedded callers can also provide standard in-memory
readers. Unsupported reader wrappers are refused before reading. A missing or failed input never means
flush everything. `--hook` cannot be combined with `--session` or `--flush`.

Only `Stop` and `SessionEnd` are accepted. Require `session_id`, `transcript_path`
and `hook_event_name`; `agent_id` must be absent or an empty string. Nonempty
subagent markers and non-string values, including `null`, are refused. The
transcript must be an absolute native path under the configured, enabled CLI root as
`projects/<project>/<canonical-session-id>.jsonl`. Alternate source roots work;
relative paths, another source and mismatched/nested session paths fail. This is
an intent/path check, not proof that the transcript exists or remains unchanged.
The worker later checks source availability, reads the session and subagents,
and refuses missing or ambiguous captures. Keep source and runtime paths stable.

`Stop` records normal flush intent. `SessionEnd` records selected flush for its
single transcript. These saved intents determine the evidence required for
manual queue sync. Workers always capture each requested session and its
subagents completely, including for normal requests. Selected flush also captures
the named paths and their subagents. Unrelated plain transcripts retain normal
large-file throttling; any all-flush request in a batch flushes every changed
tail. Chunked transcripts continue to capture all changed tails. The worker still
freezes the configured source tree and seals a complete seeded snapshot, so this
policy reduces unnecessary publication rather than source reads or capture-space
requirements. Previously backed-up subagents remain retained if removed at the
source; complete capture refreshes the eligible files still present and does not
mirror deletions. An explicitly named parent or selected path must still exist.
Previously sealed captures and commits replay unchanged.

Unknown vendor fields are ignored; message text, caller-supplied
identity and event IDs are never copied to the request. Account attribution is
read once when preparing; unavailable identity requires `--unknown-identity`.
Saved requests retain that attribution, generated event ID and timestamp on
replay. Hook preparation sends its success message to stderr and leaves stdout
empty. It uses the existing exclusive-file, isolation and durability checks;
Windows private-directory ACLs remain a caller prerequisite.

## Save and admit hook input (7c.2b.2b.1)

After `queue init`, `clauderig queue hook < hook.json` saves and admits one new
Stop/SessionEnd event. It uses the same bounded input, source validation and
account attribution as `prepare --hook`, but manages the retry record itself.
It does not read transcripts, perform remote privacy/network checks, publish,
start a worker or install hooks. Success is reported on stderr; stdout stays empty.
For fresh `queue hook`, a ten-second operation context includes the two-second
input deadline and local lock waits. Manual `recover-hooks` uses the caller's
context without an extra ten-second cap, allowing a full saved backlog to finish;
each lock wait remains limited to 15 seconds. Caller cancellation stops either
operation and preserves unfinished journal entries. A blocked filesystem call
can take longer to return.

Without a matching routing descriptor, the default inbox is `~/.clauderig/hook-inbox`.
For a matching saved runtime, hook/recovery commands without `--inbox` use the
pinned inbox, including after disabling routing. Override with `--inbox <directory>`;
use the same `--dir`, explicit `--profile` selection and inbox for every recovery.
Its existing parent must be present. First use creates a new private directory;
an existing inbox must already contain a valid journal bound to this runtime.
Source/staging/runtime trees and discovered Desktop profile trees are excluded.
Linux/macOS require private ownership and modes; Windows requires a private ACL
supplied by the caller, matching the runtime and manual-request contract. The
Windows commands do not inspect or repair ACLs; a public directory does not meet
their operating requirements. Roots must remain stable and private during operations.

Each invocation represents a new event. After an interruption or admission error,
run `clauderig queue recover-hooks` with the same options instead of replaying the
hook payload. Recovery never reads stdin or the current account. It reuses saved
event IDs, timestamps, identities and flush intent; a later account switch cannot
reattribute earlier work. A new hook saves its own request alongside any backlog
before retrying oldest-first. An unavailable account requires explicit
`--unknown-identity`; it is never silently replaced with unknown attribution.

The inbox is one canonical, checksummed `requests.json`, with at most 128 pending
requests and a 1 MiB serialized limit. These are journal limits, not a total disk
budget. Producer/recovery processes serialize under one OS lease. A request must
be durably saved before queue admission. Recovery reflushes verified journal bytes
before admission, then removes each request only after enqueue confirms acceptance.
If admission or record removal is uncertain, replay deduplicates using the saved
identity. Successfully admitted records are removed automatically; saved capture
and publication work remain in the queue. Queue status reports admitted work only,
so an empty queue is not proof of an empty producer inbox.

Full/corrupt/foreign inboxes never discard older requests. Repair queue capacity
or binding problems, then recover the inbox. Missing journals in existing inboxes,
invalid JSON, unsafe files and checksum failures block recovery; restore intact
producer state rather than recreating it. If first initialization failed before
any journal was written, no request was accepted; inspect that incomplete directory
before removing it and starting again. Input validation or a failure before durable
intent can leave the new event unsaved. Recovery can retry only records that reached
the journal; it cannot reconstruct a payload lost before persistence.

A producer request older than an explicitly advanced queue replay cutoff raises
`queue.ErrExpired` and blocks the inbox for explicit reconciliation. This is not a
transient retry: repeating recovery cannot lower the cutoff. The request may be
unaccepted or a previously completed receipt that was compacted, so the command
cannot safely discard it, assign a new event ID, or claim admission. It preserves
that record and all later records. This preview exposes no receipt-compaction
command; any future integration must stop producers and reconcile every inbox
before advancing the cutoff. If an external caller violates that ordering, keep
the journal and resolve the expired intent explicitly before resuming producers.

To finish queued work before rollback: stop hook producers, run `queue recover-hooks`
for each inbox, then `queue drain`. A successful inbox recovery confirms admission,
not remote publication. Keep the runtime and inbox intact while anything remains
unresolved. Local opt-in and checked rollback are described below; installed
hooks remain synchronous unless explicitly enabled on this machine.

### Inbox journal format

`requests.json` is versioned producer state, not an editable configuration file.
It uses the following exported field names in declaration order. All fields must
be present in the exact compact encoding emitted by Go `encoding/json`, followed
by one newline. Reordered/unknown/duplicate/case-aliased fields, whitespace edits,
invalid Unicode and checksum changes are refused. There are no omitted defaults.

| Field | JSON type and meaning |
| --- | --- |
| `Version` | Number, exactly `1`. |
| `Scope` | Nonempty string, equal to the initialized runtime's `ScopeID`. |
| `Requests` | Array of saved submissions in admission order; `[]` when empty, never `null`. At most 128 entries. |
| `Checksum` | Lowercase hexadecimal SHA-256 of this entire envelope's compact JSON with this field set to `""`, excluding the final newline. It includes every nested submission and its checksum. |

Each `Requests` element retains the existing saved-submission fields:

| Field | JSON type and meaning |
| --- | --- |
| `Checksum` | Lowercase hexadecimal SHA-256 of this submission's compact JSON with its own `Checksum` set to `""`; no trailing newline. |
| `Version` | Number, exactly `1`. |
| `Scope` | String matching the envelope and runtime. |
| `At` | Nonzero timestamp string emitted by Go `time.Time`; new events use UTC. Preserved on every retry. |
| `Identity` | Object with string fields `AccountUUID`, `OrganizationUUID`, `Email`, in that order. UUIDs are canonical when nonempty; `""` means unavailable. All three empty strings record explicit unknown attribution. Nulls are refused. |
| `Request` | Object with string fields `EventID`, `SessionID`, `ProvenanceID`, followed by the `Flush` object. IDs are preserved on retry; session IDs are bounded, trimmed and lowercase; provenance must match the identity. |
| `Request.Flush` | Object with string `Mode` followed by `Paths`. Hook modes are `"normal"` or `"selected"`; all-flush is not accepted in this inbox. |
| `Request.Flush.Paths` | Normal mode accepts `null` or `[]` (new events emit `null`). Selected mode requires an array containing exactly one native parent-transcript path; admission validates that path against the runtime/session. |

Illustration only: placeholders and indentation below are not a writable journal.

```json
{
  "Version": 1,
  "Scope": "<runtime-scope>",
  "Requests": [{
    "Checksum": "<submission-sha256>",
    "Version": 1,
    "Scope": "<runtime-scope>",
    "At": "2026-09-10T12:00:00Z",
    "Identity": {"AccountUUID": "", "OrganizationUUID": "", "Email": ""},
    "Request": {
      "EventID": "<generated-event-id>",
      "SessionID": "s",
      "ProvenanceID": "<unknown-identity-provenance>",
      "Flush": {"Mode": "selected", "Paths": ["/home/you/.claude/projects/acme/s.jsonl"]}
    }
  }],
  "Checksum": "<envelope-sha256>"
}
```

Every save/reflush/removal replaces the complete canonical envelope through the
shared durable writer under the inbox lease. Removal recomputes the envelope
checksum after queue confirmation; it does not edit nested submission identity or
checksums. A successfully empty journal remains present and bound to the runtime.

## Next

The internal runtime bridge (7c.1), explicit manual command (7c.2a) and bounded
hook-request preparation (7c.2b.1) are available. Opt-in hook installation,
ordinary-sync routing and end-to-end stop/drain/rollback remain 7c.2b.2b.2.
Managed hook admission and inbox recovery are available explicitly.

## Local hook opt-in and rollback (7c.2b.2b.2)

First stop Claude sessions, manual syncs and any other producers or workers.
Recover any previously used explicit inboxes before changing producer destinations.
Install the standard hooks with `clauderig hooks install`, perform an ordinary
sync to establish shared history, and initialize the queue. Use the same runtime
and every local Desktop profile required by `queue sync`. Then:

```sh
clauderig queue enable-hooks
clauderig queue hook-status
clauderig queue run
```

`enable-hooks` checks the initialized binding and complete Desktop profile
selection. It requires exactly one standard owned command for SessionStart, Stop
and SessionEnd in user settings, with no stale command or matcher. It does not
rewrite settings. Each routed sync invocation rechecks the current installed hook
plan using a regular, non-symlink settings file of at most 1 MiB. The read checks
cancellation between chunks and refuses larger/growing inputs; missing, disabled,
malformed or changed hooks fail closed without admission
or synchronous fallback. Recovery and rollback remain available when hook settings
need repair. Enabling creates or reflushes the private inbox before saving local
routing. `--inbox` chooses another inbox with an existing parent;
`--unknown-identity` deliberately records unknown attribution for every hook.
Repeating the same active enable is safe. Changing active options requires a completed
disable first. A retained disabled descriptor is not first-ever initialization:
every re-enable requires its original runtime and inbox to remain intact, its
inbox empty and its queue idle under worker/transaction ownership. Missing or
corrupt retained state, pending requests/work and an active old worker block even
a request for another destination. Old recovery records are never deleted.
Initialization errors may leave an incomplete inbox: inspect and restore its
journal rather than discarding potentially saved requests.

The existing portable commands remain `clauderig pull`, `clauderig sync --hook`
and `clauderig sync --flush`. Each machine chooses routing through its own
`~/.clauderig/queue-hooks.json`; this state stays outside capture roots. Syncing
settings does not opt another machine in. SessionStart pull remains synchronous.

With routing enabled, Stop and SessionEnd invoke the durable hook producer. The
complete input must arrive within two seconds and 128 KiB; the ten-second hook
budget includes input, settings validation and routing lock contention. Filesystem
cancellation is cooperative between operations: a stalled kernel filesystem call
can exceed that budget. `--hook` requires Stop; a
payload to `--flush` requires SessionEnd. Empty input to `--hook`, blank or
malformed documents, mismatched events, corrupt routing, missing inbox state or
changed bindings fail without falling back to synchronous publication. Failure
before durable intent can still leave that new event unsaved; the existing inbox
recovery limitations apply. Successful hook diagnostics go to stderr only.

Manual `sync` delegates to the supervised `queue sync` path. A terminal or truly
empty input to `sync --flush` means all changed tails. `--dry-run` ignores hook
input and never admits or acknowledges requests. It still performs queue-sync
startup/privacy checks. Combining `--hook` and `--flush` is refused in queued mode.
Manual sync keeps its caller context and releases the routing lease before long
publication, allowing hooks to keep enqueueing; worker/staging ownership protects
its capture. It does not drain the producer inbox or every queued request.

No worker is launched automatically. After a process or machine restart, use
`hook-status` to recover the saved options, run `recover-hooks`, then restart
`queue run` with the same runtime/profiles. Default hook/recovery commands follow
the matching descriptor’s inbox; explicit `--inbox` selections remain caller-owned
and must each be recovered separately. Direct `queue hook` still uses its own
`--unknown-identity` flag; the descriptor’s identity choice applies to installed
hooks routed through `sync`. Stop producers before deliberate
reconciliation. A stopped worker or empty queue does not prove the inbox is empty.
The OS process-fence recovery requirements still apply after an unclean restart.

To restore synchronous operation:

1. Stop Claude sessions, manual syncs and all other producers, including direct
   callers of `queue hook`, `prepare`/`enqueue` and `queue sync`.
2. Run `queue recover-hooks` with the saved runtime/profiles/inbox.
3. Drain the queue and stop every worker. Repair blocked/delayed work first.
4. Run `queue disable-hooks` with the saved runtime/profile flags.

Disable, including a retry against a retained disabled descriptor, holds the
routing and inbox leases, reflushes an empty inbox, then takes
queue worker/transaction ownership, reflushes queue state and requires no pending
batches before saving a disabled descriptor. It refuses an active worker, even
if that worker currently has no batch. It never drops pending intent, queue work,
artifacts or receipt history. Direct producers and manual sync startup must be
stopped by the operator; the toggle cannot stop an external caller from starting
new work after its checks. Stable private filesystem roots and no concurrent
settings/config edits are prerequisites.

If enable/disable reports a write error after a rename, the descriptor may already
show the requested state. Inspect `hook-status`, keep producers stopped, and retry
the same command to reflush it. Routed sync also reflushes an enabled descriptor
before using it. Never delete/edit the descriptor or switch to a v1 binary to
bypass rollback. A malformed descriptor fails closed even when its damaged bytes
appear to say disabled. A successful disable retains it for inspection.

### Routing descriptor format

`queue-hooks.json` is a private regular, single-link file of at most 128 KiB.
Linux/macOS check effective ownership and private modes; Windows callers must
provision private inherited ACLs, which the command does not inspect or repair.
Symbolic links are refused. It uses exact compact Go JSON encoding plus one LF,
with these fields in declaration order:

| Field | Writer, purpose and default | Validation and lifecycle |
| --- | --- | --- |
| `Version` | `enable-hooks` supplies integer `1`; no omitted or null form. Identifies the routing format. | Reader accepts only `1`; unknown versions block all routing/toggle commands. No automatic migration. |
| `Enabled` | Enable writes `true`; disable writes `false`. With no descriptor, routing is off. Persisted values have no omitted/null default. | Must be a JSON boolean. Routed reads and same-command retries retain the value; only an explicit toggle changes it. A disabled record still receives full file/encoding/checksum validation. |
| `Runtime` | Enable saves the opened runtime's canonical absolute directory, selected by `--dir` or `~/.clauderig/queue-runtime`. No null/empty default in saved data. | Reader requires an absolute path. Enabled operations reopen it against current configuration and the lifecycle scope; disable requires the selected runtime to match. Runtime isolation/private-store rules apply. The descriptor's entire 128 KiB limit bounds the string. |
| `Inbox` | Enable resolves `--inbox` to an absolute path, defaulting to `~/.clauderig/hook-inbox`. No null/empty saved default. Pins producer recovery location. | Reader requires an absolute path. Enable/use/disable enforce capture/staging/runtime exclusion and keep the descriptor outside the inbox, comparing actual directory identities for case/alias handling. Existing journals must match `Scope`; missing/corrupt state blocks active routing. Bounded by the descriptor's 128 KiB limit. |
| `Profiles` | Enable saves sorted explicit `--profile` names. No selection writes `null`; an empty array is also readable and preserved. | Elements must be strings. Enable verifies complete local Desktop coverage and runtime binding. Reopen/manual capture revalidate their respective profile/binding rules. No silent sorting, dropping, renaming or rewriting on read; total bytes are bounded by the descriptor limit. |
| `Scope` | Enable generates the runtime lifecycle's SHA-256 binding digest using `ScopeID`; never taken from hook input. No empty/null default. | Reader requires a nonempty string and valid descriptor checksum. Enabled operations compare it with the reopened runtime's scope; a foreign scope fails. It remains unchanged on disable and reflush, preserving the association with inbox/queue records. |
| `UnknownIdentity` | Enable saves the explicit `--unknown-identity` choice, default `false`; no omitted/null saved form. Determines whether every installed hook bypasses live identity lookup. | Must be a JSON boolean. It is not an identity observation or fallback policy. Retained on disable/reflush; changing it while enabled requires completed rollback and a new enable. Existing producer events always retain their original attribution. |
| `Checksum` | Every descriptor write computes SHA-256 hex over the compact typed object with this field set to the empty string, without LF. Generated locally, never supplied by the hook. | Required string matching the recomputed digest. All preceding fields participate, including `Enabled`. It changes on an explicit toggle but not a byte-preserving reflush. Detects accidental damage, not same-user tampering. |

Every field is required in the exact persisted encoding; only `Profiles` permits
`null`. These are local routing choices, not synchronized user configuration or
queue event IDs. `enable-hooks` owns creation and option selection; checked
`disable-hooks` owns the transition back to synchronous behavior. Active reflush
and idempotent retries write exactly the same typed values, including retained
disabled options. No read infers replacement identity, runtime or profile defaults.

The common private-state helper owns regular-file/private-mode/link/size checks,
canonical JSON reads and bounded durable writes. Routing and inbox callers still
own their paths, schema validation, checksums, limits and lifecycle transitions.
There is no shared destination chooser that could redirect one format into the
other. Both formats retain their existing byte representation. Mutating routing
commands share the lease/descriptor loader and runtime/path validation; disable
and retained re-enable also share the intact-empty-inbox check. First-enable,
unchanged active enable, rollback and producer-reflush policies stay in callers.

A valid disabled descriptor does not require its runtime to be reopened merely
for ordinary synchronous sync. Every `disable-hooks` retry still checks the pinned
runtime, inbox and worker/queue idleness before reflush. On a fresh home with no
configuration parent, disable reports that routing is not enabled and creates no
state. Re-enabling validates current configuration and complete profile coverage
for the requested destination, then reopens and reconciles the retained runtime
with its saved profile selection. Both bindings must still validate; changed
configuration that prevents reopening the old runtime blocks replacement. Restore
the prior configuration and reconcile rather than deleting the descriptor. This
preview does not provide a bypass for incompatible lifecycle migration.

For a destination change, both inbox leases are acquired before old-queue
maintenance ownership. The new inbox is initialized/reflushed and the new routing
descriptor is written only inside that idle check. Same-directory aliases share
the existing inbox lease. A pre-replacement failure preserves the old descriptor;
a post-replacement uncertainty is retried using the same requested options. The
old inbox and queue remain available after successful retargeting. Do not manually
edit retained fields or replay a descriptor from another lifecycle: enabling/use
still enforce scope association.
Fixed format-v1 fixtures cover enabled and retained-disabled records on Unix and
Windows; reading and reflush preserve their canonical bytes and every field.
Pre-/post-replacement error tests cover enable, disable and active reflush with
unchanged queue/inbox records; truncated descriptors are refused and never reset.

Unknown/duplicate/case-aliased fields, reformatting, invalid Unicode and checksum
changes are rejected by exact re-encoding. The checksum detects accidental damage,
not malicious modification by the same user. `hook-status` displays the descriptor
without claiming inbox, worker or remote health. The routing lease is a stable
sibling lock; never remove it while operations may be running.
