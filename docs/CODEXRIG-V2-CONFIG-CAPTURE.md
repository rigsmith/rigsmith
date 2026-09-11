# Codex config file capture (8b.2)

`adapter.CaptureConfig` connects the TOML codec to real source files. It accepts
an explicit Codex-home root and returns a complete in-memory batch or an error.
It neither writes files nor creates tool state, executes Codex/helpers, or
publishes anything. The preview command remains `codexrig inspect`.

## Selection and limits

Only direct `config.toml` and valid `NAME.config.toml` children are selected,
using the existing Codex classification policy. Profiles remain separate files;
there is no flattening of overlays. Case-colliding profile names and Windows
reserved device basenames are refused on every supported OS. Selected filenames
with recognizable credential signatures are also refused before reading them. Unknown filenames,
credentials, hooks, instructions, skills, sessions and databases are not opened.
The reader never descends into subdirectories.

Capture is bounded by 4,096 direct directory entries, 32 selected files, 1 MiB per
file, and 8 MiB each for aggregate raw and sanitized bytes. Directory enumeration
stops at its limit without collecting an unbounded list. Oversized files fail
before reading when metadata already exceeds the limit; reads also enforce the
limit if a file grows. The codec's own output/depth limits remain in effect.

Every selected name must be a regular file. A selected directory, link, unreadable
file, invalid TOML document or secret finding fails the whole batch. Missing roots
and vanished files are errors, not empty snapshots or deletion instructions. A
present directory containing no config files returns an explicit empty file list;
this service never instructs pruning.

## Shared read boundary

`internal/agentrig/files.Source` supplies bounded, read-only mechanics for Linux,
macOS and Windows. It pins a root directory handle and verifies that its named
root still identifies the same non-link directory. Ancestor aliases such as the
macOS temporary-directory path are allowed. The caller chooses direct-child
names and limits; Source applies no vendor selection or codec.

Before reading, Source checks regular-file type, size, handle identity and the
current directory entry. After reading, it checks identity, size, mode, timestamp,
byte count and root identity again. File content errors expose fixed categories,
not source text or private filenames. Cancellation is checked between directory
batches, 32 KiB read blocks and capture steps. An individual OS syscall may still
outlast cancellation.

On Linux/macOS, direct `openat` from the pinned directory uses no-follow,
nonblocking and close-on-exec flags. This matters because Go's `os.Root.OpenFile`
resolves internal links even when supplied the Unix no-follow flag. Nonblocking
open also prevents a regular-file-to-FIFO swap from waiting for a writer. On
Windows, handle-relative `NtCreateFile` opens the direct child with reparse
processing disabled, non-directory/read-only access, no handle inheritance and
complete-if-oplocked behavior. Reparse, offline and non-disk handles are refused
before reading. Direct-child validation excludes device namespaces. Unsupported
platforms refuse opening a Source and have no weaker open fallback. Both readers
still depend on OS syscalls that can outlast cancellation. The Windows flags
follow the [Microsoft NtCreateFile contract](https://learn.microsoft.com/en-us/windows/win32/api/winternl/nf-winternl-ntcreatefile).

After processing every file, the Codex adapter re-reads them and compares
in-memory SHA-256 fingerprints. This catches changes to earlier files, including
same-size edits with restored modification times. It then re-lists selected
config names and checks the root again. New/deleted profiles refuse the batch;
unrelated history/log activity is ignored. Source fingerprints are temporary and
are never added to the returned batch or diagnostics. Reader and adapter change detection share
the same error identity (`files.ErrSourceChanged`, also exposed as
`adapter.ErrConfigSourceChanged`) for future retry classification.

These checks detect observed changes. They are not an atomic transaction with
Codex or a defense against every mutation by an uncooperative/hostile writer. A
writer can change a file after its last check. Future sync coordination must
account for later changes and seal accepted bytes under the shared artifact
lifecycle; a failed capture must never be published as a partial result.

## Output and remaining gates

The batch contains relative native names and codec output only. It has no source
root metadata or raw-file fallback and is not yet a serialized backup format.
The [TOML codec's limitations](CODEXRIG-V2-CONFIG-CODEC.md) still apply: unknown
short secrets cannot be inferred reliably, and omitted helper/credential values
may need local configuration. [TOML path policy](CODEXRIG-V2-CONFIG-PATHS.md)
automatically omits machine-local settings and refuses explicit unclassified local
references in every selected file; arbitrary strings are not proven portable.

Guarded destination file replacement, usable-config
validation, structured hooks/customizations, independent CodexRig state/repository
and sync/restore commands remain in 8b. The internal codec/capture changesets are deferred until a user-facing workflow
uses them; these prerequisites should not announce unavailable features.
No existing Claude caller is switched to
the new Source API. Claude's file handling, backup defaults and state are unchanged.

Tests use synthetic directories only. They cover selection and unchanged source
bytes, links/special files, cancellation between read blocks, root replacement,
limits, codec refusal with no partial output, metadata-preserving content edits,
profile arrivals/deletions and unrelated source activity. Linux/macOS also test
FIFO open and internal-symlink refusal directly. Native CI covers the three
supported platforms alongside the pinned Claude compatibility suite.
