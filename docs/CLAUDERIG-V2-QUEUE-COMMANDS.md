# Explicit queued Claude commands (7b)

V2 exposes a foreground queue workflow for deliberate testing and use. It does
not install a worker or route hooks. Ordinary `sync`, `pull` and hooks keep their
existing synchronous behavior. Use v2 clients for operations sharing staging.
Actual OS reboot/hibernation validation remains a general-release gate.

Bare `queue` on a terminal offers init, status, run and drain. Prepare/enqueue/retry
require explicit command arguments; off a terminal, bare `queue` prints help.

## Start and accept work

Run an ordinary `clauderig sync` first to initialize local and remote history.
The queue uses the configured private HTTPS GitHub/GitLab remote and existing
Git credential helpers (`gh auth setup-git` where appropriate). Privacy checks use
the existing `gh`/`glab` or token verification. It adds no authentication store or
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
canonicalizing existing ancestors, including symlink aliases. `prepare` creates a new file with mode 0600, refuses an
existing file and flushes the saved request before succeeding. Keep Windows
runtime/request files under a private user directory: inherited ACLs are a caller
prerequisite, not validated or repaired by these commands. Linux/macOS runtime
reopening checks effective ownership and private directory/descriptor modes.

`prepare` reads account identity once, generates one event ID and timestamp, and
pins the runtime binding. It saves intent and validated attribution, not transcript
bytes. `--session` is required; `--flush` captures every changed transcript tail.
Without it, normal capture policy applies. Valid email/organization observations
are retained even without an account UUID. A completely empty identity requires the explicit
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

Milestone 7c connects opt-in hooks, manual-sync coverage and stop/drain/rollback.
Until then, explicit queue work and synchronous sync can serialize on staging,
but a synchronous sync does not acknowledge pending queue events. Request-file
preparation is the manual producer contract, not the future hook input protocol.
