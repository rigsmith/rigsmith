# Transcript storage and publication scanning

New configurations default to chunking on. Existing configurations with no
`chunkTranscripts` key use auto: they follow the repository setting, keeping an
unversioned legacy backup plain until it is migrated. Explicit false opts out.
Chunking changes only the backup repository; Claude Code continues to read and
write native JSONL.

## Migrate an existing backup

Upgrade **every machine sharing the backup repository** to a clauderig release
that supports `chunkTranscripts` before migrating it. New configurations start
with it enabled, so also check this before connecting a freshly configured
client to a legacy shared backup. Older binaries do not
understand chunk indexes and can restore an index as though it were a transcript.
Do not let an older client's sync or restore hooks operate on a chunked backup.

```sh
clauderig config set chunkTranscripts true
clauderig sync
```

The next sync converts existing staged transcripts larger than 8 MiB, including
ones contributed by other machines. Smaller transcripts stay native. Large new
or changed transcripts are chunked immediately, including their latest tail;
`retention.largeFileBytes` throttling does not apply while chunking is enabled.
The hook's normal debounce applies after migration; an explicit storage-mode
change bypasses it, and a manual sync runs immediately.

The repository records its mode in `clauderig-storage.json`. Updated clients
with no local override follow that setting when they next sync. An explicit
local `true` or `false` overrides it, so keep overrides consistent across machines.
To return a machine to following the repository:

```sh
clauderig config set chunkTranscripts auto
```

## Roll back

Using the updated binary, run:

```sh
clauderig config set chunkTranscripts false
clauderig sync
```

This reconstructs existing chunked snapshots as plain JSONL and removes their
chunk directories from the current tree. Clear any `true` overrides on other
machines with `config set chunkTranscripts auto` so they do not enable it again.
Rollback refuses before changing the tree if a chunked snapshot exceeds the
configured native-file cap. Keep chunking or increase `retention.maxFileBytes`
only if the remote permits the resulting blobs. A disabled or excessively large
cap can still produce native Git blobs too large for the remote host. Existing Git history still contains the chunked revisions;
use an updated binary to read or restore those revisions.

## Layout and guarantees

`cli/projects/<slug>/<id>.jsonl` becomes a version-1 JSON index describing the
original byte length and an ordered list of SHA-256 hashes and part lengths.
Its chunks live beside it, in `<id>.jsonl.chunks/<hash>.part`. Each full part is
4 MiB; the last part can be shorter. Boundaries are byte offsets and may split a
JSON record or UTF-8 character. Reassembly preserves the exact original bytes,
including a last record with no newline.

Completed chunks are reused when a transcript grows. The short tail is replaced,
and newly completed chunks are added. Git can still retain previous tails in
history; chunking reduces repeated large blobs, but does not put a hard bound on
repository growth or rewrite existing history. Existing retention and history
maintenance remain useful. Chunk mode requires a file cap of at least 4 MiB (or
no cap); the logical transcript can exceed the usual 50 MiB native-file cap.

Manual and automated syncs use the same staging lock; a manual sync waits up
to 15 seconds for an existing writer and reports a retry if it remains busy.
Chunks are written before an atomically replaced index. Failed writes leave the
previous snapshot readable. Conversion can be rerun after interruption. Readers
verify part sizes and hashes; restore writes a temporary native file and replaces
the destination only after successful reassembly. A corrupt or missing part fails
that restore without truncating the existing destination. Retention and session
deletion remove a transcript and its chunks together.

Search, session titles/activity, the ledger, peek, historical ledger recovery,
and restore read the logical transcript. Git history readers load indexes and
parts from the same revision. Divergent chunk indexes, including native/chunk
conflicts during migration, remain unresolved instead of being line-unioned.
Resolve or abort those merges before syncing again. Plain transcript merge
policies are unchanged.

## Stricter scanning

Sync scans complete staged text streams with bounded memory, including large
transcripts, unchanged files, remote-only files and stored chunks. It recognizes
credential prefixes, JWTs, bearer tokens, PEM private-key headers and common
ASCII JSON escapes. Overlapping reads cover signatures split across scan or
storage boundaries. Existing credential-filename and auth-config rules also
apply. Read and chunk-integrity failures stop publication. The command checks
the staged tree again before committing and pushing, including after merges.
Diagnostics report file paths and finding kinds, not credential values.

Scanning is always on. Without scrubbing, recognized credentials cause a refusal.
To scrub supported credential signatures from staged transcripts first:

```sh
clauderig config set redactTranscripts true
clauderig sync
```

Live transcripts are never edited. Private-key blocks and signatures the scrubber
cannot safely rewrite still cause refusal. Scanning uses known patterns, not
entropy guesses over conversation prose; it cannot recognize every possible
secret or decode arbitrary binary/encrypted content. It audits the current
staged tree, not all historical Git commits. Enabling scanning or redaction does
not remove credentials from previously committed history.
