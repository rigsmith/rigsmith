# Codex config restore preparation (8b.4)

`adapter.PrepareConfigRestore` prepares an in-memory restore plan against an
existing Codex home. It does not write config files, create a missing home,
publish destination data, invoke Codex, or expose a new CLI command.
[Application](CODEXRIG-V2-CONFIG-APPLY.md) now consumes these plans for file
replacement. A concrete validator for supported Codex versions still follows.

## Preparation contract

1. Require a Codex-home destination and a non-nil `ConfigRestoreValidator`.
   Validate all incoming names and TOML before opening the destination. Only base
   and named profile files are accepted; traversal, non-config files, duplicates,
   case collisions, reserved Windows names and unsafe backup content refuse the
   entire preparation. Input buffers are not retained.
2. Pin the destination using the shared bounded source reader. Read all selected
   local base/profile files, including profiles absent from the backup. Missing
   homes, selected links/special files and malformed local TOML fail. Case aliases
   such as `CONFIG.TOML` or `work.CONFIG.TOML` fail on every OS; incoming profile
   names cannot alias a differently cased destination name.
3. Apply the TOML codec's secret/path-preserving merge to incoming files. Keep
   destination files absent from the backup. For semantic no-ops, retain original
   bytes, comments and formatting. Changed files use the codec's encoding. Empty
   backup sets express no deletion intent.
4. Check destination fingerprints and the selected name set. Pass detached copies
   of the complete, sorted proposed base/profile set to the required validator.
   The set includes unchanged and retained files, so validation can evaluate base
   settings together with every profile. A callback failure returns the fixed
   `ErrConfigValidation`, without echoing its potentially private diagnostics.
   Context cancellation remains identifiable.
5. Check the destination again after validation, then return the plan. A change
   or refusal anywhere returns no plan and performs no filesystem mutation.

Each document is limited to the codec's 1 MiB cap. Incoming raw and normalized
bytes, local raw bytes, and complete proposed bytes each have independent 8 MiB
aggregate caps. Incoming, local and combined file sets are each limited to 32
files. Directory enumeration retains the existing 4,096-entry bound. Validation
callbacks receive a context and must cooperate with cancellation; the API does
not forcibly interrupt callback code or OS syscalls.

## Private plans and validation responsibility

`ConfigRestoreValidator` is an explicit integration boundary, not a bundled Codex
schema validator. A production caller must supply supported-version and
destination checks for usable base/profile configuration, including missing
helpers, credentials, referenced artifacts and provider/agent definitions. A nil
validator fails closed. The synthetic accepting validators in tests do not make
this API ready for a user-facing restore command.

Validation inputs contain destination credentials and paths. The callback must
not log or publish them or execute configured helpers. Its buffers are detached:
mutating or retaining them cannot alter the plan. Incoming caller buffers and
change summaries are detached as well.

The plan keeps proposed private bytes and original fingerprints unexported. Its
ordinary and Go-syntax formatting show only a fixed private-plan label, and JSON
serialization exposes no fields. `Changes` reports only incoming relative names
and `create`, `update`, or `unchanged` actions. It does not return merged contents
or hashes. Every successful plan must be closed; close releases the pinned handle
and drops private buffers, and later checks fail with `ErrConfigPlanClosed`.
Dropping references is not a runtime memory-zeroization guarantee.

## Destination checks and the next write phase

`Check` re-reads the original selected files and compares SHA-256 fingerprints,
then re-lists names and checks the pinned root. It detects observed content edits,
including same-size edits with restored mtimes, profile arrivals/removals, name
aliases and root replacement. Unrelated history/auth/log activity is ignored and
those files are not opened. The checks compare content, not retained per-file
inode/mode identity across the lifetime of the plan.

These checks are not an atomic compare-and-swap with other applications. A file
can change after its last check. The [application phase](CODEXRIG-V2-CONFIG-APPLY.md) coordinates participating
writers, rechecks the plan before each replacement, guards file identity and
metadata, and reports partial or uncertain outcomes. Nonparticipating editors
still require caller coordination; these checks are not a multi-file transaction.

Synthetic tests cover full-set validation, local credential preservation, no-op
formatting, immutable input boundaries, private representations, malformed or
unsafe late files, limits, selected links/directories, name aliases, cancellation,
concurrent destination edits, profile churn and pinned-root replacement. When
Windows prevents moving the open root, tests verify that protection and confirm
that closing the plan releases the directory. Claude
behavior and the shared file-writing helpers are unchanged.
