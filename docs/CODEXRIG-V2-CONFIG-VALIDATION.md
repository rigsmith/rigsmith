# Codex versioned config validation (8b.6a)

`adapter.PrepareVersionedConfigRestore` adds an offline, pinned validation stage
to restore preparation. It requires both a supported version and the existing
mandatory destination validator. It exposes no command and does not run Codex,
execute configured helpers, read authentication stores or fetch remote schemas.

## Supported contract

The initial structural policy supports **exactly Codex CLI 0.144.6**. The caller
must supply that canonical version string from trusted destination/version
selection. This API does not discover or verify an installed binary itself.
Older, newer, prerelease and decorated version strings fail closed before the
source directory is opened. Support for a release means the pinned structural
policy is available, not that all Codex workflows have been certified.

The embedded schema is the unmodified Draft 7 schema from release commit
`5d1fbf26c43abc65a203928b2e31561cb039e06d`. Its hash, license and provenance are in
[the schema directory](../internal/codexrig/configcodec/schema/README.md).
`github.com/santhosh-tekuri/jsonschema/v6` is pinned to v6.0.3 for Draft 7
validation. The existing indirect schema dependency does not resolve this
snapshot's Draft 7 `definitions` references correctly. URL/file loading is
disabled, and validation never refreshes its schema from the network.

## Full-set validation

1. The existing preparation pipeline validates backup names/bytes, pins the
   destination and merges local secrets/paths. It includes retained profiles in
   the proposed set and passes detached private bytes to validation.
2. Validate the base configuration against the release schema. An absent base is
   an empty layer. Then independently overlay each named profile on that base and
   validate the effective result. Tables merge recursively; arrays and scalars
   replace. A profile never inherits another profile's definitions. The release's
   memory-setting alias is canonicalized within each parsed layer before merge;
   proposed restore bytes are unchanged. Permission-domain normalization remains
   part of destination/runtime validation.
3. Enforce native TOML numeric kinds and the schema's signed/unsigned widths,
   including uint16 ports and int32 values. A float such as `1.0` cannot satisfy
   an integer field. Nonfinite floats and TOML date/time values are refused;
   they are not silently converted to strings or JSON objects.
4. Reject legacy `profile` and `profiles` fields, even though the release schema
   retains them. Check that an explicitly selected provider is built in or is
   defined in the effective configuration. Built-in provider IDs are pinned to
   the release; model availability and provider authentication are not probed.
5. Only after structural checks pass, invoke the required destination callback.
   Its failure refuses preparation. As before, private callback/schema error
   details do not escape; cancellation stays identifiable and source checks run
   after validation. Application still consumes the resulting private plan.

The codec-level `ValidateConfigSet` operates on a base document and profile byte
slices; filename/classification checks belong to the adapter. Limits remain 32
files, 1 MiB per document, 8 MiB aggregate input and the codec depth limit. The
base/profile overlay gets another depth check. Parsing/schema calls are bounded
but not forcibly interrupted mid-call; cancellation is checked between documents
and after validation. No config bytes, local paths or validation values appear in
returned diagnostics. Unknown fields follow the exact schema: closed objects
reject them; explicitly open objects remain open.

## Remaining destination validation (8b.6b)

This is a concrete structural validator, **not a complete Codex startup check**.
The required destination callback still needs a production implementation covering
managed/system/project layers, permissions semantics, runtime-only provider/MCP
constraints, referenced files and agent definitions, helper availability and
credential readiness. It must not execute configured helpers or expose private
diagnostics. Model-dependent values such as reasoning effort also remain outside
the schema's guarantees. The general preparation API remains an injection boundary;
production restore wiring must choose the versioned entry point and complete these
checks. Accepting test callbacks are not a production readiness implementation.

Stage 8b.6 remains in progress until that destination layer is implemented.
Customization portability, independent state/repository, user-facing commands and
interrupted-restore recovery remain later roadmap work. Claude callers are
unchanged, and no installer or live configuration is modified.

## Evidence

Synthetic tests cover schema provenance/local references, nested-field and enum
refusal, numeric type/width boundaries, legacy profiles, provider lookup across
independent profile layers, private-value preservation, input limits,
cancellation, concurrent validation, callback ordering, retained invalid profiles,
and applying an accepted versioned plan. Native repository CI runs on Linux,
macOS and Windows; these tests do not launch the Codex binary or prove native
Codex startup behavior.

The [official config reference](https://learn.chatgpt.com/docs/config-file/config-reference)
links the published schema. The [official profile documentation](https://learn.chatgpt.com/docs/config-file/config-advanced)
describes separate profile files and removal of legacy profile selection. The
release schema and runtime code were checked at the pinned commit, rather than
assuming the current public schema describes every installed version.
