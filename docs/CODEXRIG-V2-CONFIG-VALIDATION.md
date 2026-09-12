# Codex versioned config validation (8b.6)

`adapter.PrepareVersionedConfigRestore` adds an offline, pinned validation stage
to restore preparation. It requires both a supported version and the existing
mandatory destination validator. It exposes no command and does not run Codex,
execute configured helpers, read authentication stores or fetch remote schemas.

## Supported contract

The pinned validation policy supports **exactly Codex CLI 0.144.6**. The caller
must supply that canonical version string from trusted destination/version
selection. This API does not discover or verify an installed binary itself.
Older, newer, prerelease and decorated version strings fail closed before the
source directory is opened. Support for a release means the pinned validation
policy is available, not that all Codex workflows have been certified.

The embedded artifact is the unmodified Draft 7 schema from release commit
`5d1fbf26c43abc65a203928b2e31561cb039e06d`. Its hash, license and provenance are in
[the schema directory](../internal/codexrig/configcodec/schema/README.md).
`github.com/santhosh-tekuri/jsonschema/v6` is pinned to v6.0.3 for Draft 7
validation. The existing indirect schema dependency does not resolve this
snapshot's Draft 7 `definitions` references correctly. URL/file loading is
disabled, and validation never refreshes its schema from the network. A private
parsed copy now fills five known permission-map gaps from the same release; see
[layered validation](CODEXRIG-V2-LAYERED-VALIDATION.md).

## Full-set validation

1. The existing preparation pipeline validates backup names/bytes, pins the
   destination and merges local secrets/paths. It includes retained profiles in
   the proposed set and passes detached private bytes to validation.
2. Validate the base configuration against the release schema. An absent base is
   an empty layer. Then independently overlay each named profile on that base and
   validate the effective result. Tables merge recursively; arrays and scalars
   replace. A profile never inherits another profile's definitions. The release's
   memory-setting alias is canonicalized within each parsed layer before merge;
   proposed restore bytes are unchanged. Selected permission-domain declaration
   checks follow native normalization and glob syntax. Selected endpoint checks
   also reject blank explicit proxy/SOCKS addresses and invalid allowed Unix-socket
   path declarations after inheritance. Socket maps merge by exact key; only
   effective allows require Unix-style or destination-native Go absolute paths,
   with NUL refusal as restore policy. These checks include selected disabled
   networks; inactive profiles remain uncompiled. Full policy compilation remains
   destination/runtime work.
3. Enforce native TOML numeric kinds and the schema's signed/unsigned widths,
   including uint16 ports and int32 values. A float such as `1.0` cannot satisfy
   an integer field. Nonfinite floats and TOML date/time values are refused;
   they are not silently converted to strings or JSON objects.
4. Reject legacy `profile` and `profiles` fields, even though the release schema
   retains them. Check that an explicitly selected provider is built in or is
   defined in the effective configuration. Reject configured `openai`, `ollama`
   and `lmstudio` declarations before catalog merge. Check every custom provider,
   including unselected ones, for a nonblank name and conflicting auth settings;
   AWS configuration is reserved for Bedrock. Bedrock permits AWS profile/region
   overrides and explicitly default-valued fields, but no other overrides.
5. Check every MCP server, including disabled servers, for a command or URL and
   transport-compatible fields. Presence matters: HTTP `args=[]` and stdio
   `http_headers={}` are invalid too. Validate environment sources and finite,
   nonnegative timeouts within Rust's duration range. Seconds take precedence
   over the legacy startup milliseconds field; invalid seconds cannot fall back
   to milliseconds. Base and each profile's effective configuration must pass.
6. Only after schema and these runtime rules pass, invoke the destination callback.
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
reject them; known permission-map gaps receive the documented supplemental value
constraints, and other explicitly open objects remain open.

## Remaining destination validation (8b.6b)

This validates schema and selected runtime rules, **not complete Codex startup**.
Provider declarations/auth combinations, Bedrock overrides and MCP transport,
environment-source and timeout rules now have concrete offline checks. These use
the same exact release as the schema; [source provenance](../internal/codexrig/configcodec/schema/README.md)
records the native validation order. In particular, reserved provider declarations
are rejected before the catalog merge; the initial 8b.6a description of ordinary
collisions being ignored was incomplete and is corrected here.

Layer composition and permission catalog/selection checks are now available via
[`PrepareLayeredConfigRestore`](CODEXRIG-V2-LAYERED-VALIDATION.md), using trusted
caller-supplied context. The required destination callback still needs production
discovery/freshness checks for managed/system/project sources, full permission
compilation and other managed requirements, complete native proxy URL parsing
and permissive host/port fallback, bind-address behavior, socket availability and
platform support, remaining runtime constraints,
referenced files and agent definitions, helper availability and
credential readiness. It must not execute configured helpers or expose private
diagnostics. Model-dependent values such as reasoning effort also remain outside
the schema's guarantees. The general preparation API remains an injection boundary;
production restore wiring must choose the versioned entry point and complete these
checks. Accepting test callbacks are not a production readiness implementation.
An allowed helper or URL only has valid configuration fields: this layer does not
check its existence, reachability, credentials, environment values or launch
behavior. In particular, a nonblank proxy address is not proof that the native
URL parser accepts it, and an absolute socket path is not proof of availability.
Endpoint checks perform no path lookup, address resolution or socket connection;
see the [endpoint contract](CODEXRIG-V2-LAYERED-VALIDATION.md#selected-network-endpoint-declarations)
for inheritance and validation limits. No inherited environment or live
authentication store is read.

Stage 8b.6 remains in progress until that destination layer is implemented.
Customization portability, independent state/repository, user-facing commands and
interrupted-restore recovery remain later roadmap work. Claude callers are
unchanged, and no installer or live configuration is modified.

## Evidence

Synthetic tests cover schema provenance/local references, nested-field and enum
refusal, numeric type/width boundaries, legacy profiles, provider lookup across
independent profile layers, private-value preservation, input limits,
cancellation, concurrent validation, callback ordering, retained invalid profiles,
and applying an accepted versioned plan. Runtime regressions explicitly verify
that their fixtures pass the bundled schema first, then check native cross-field
refusals, allowed Bedrock defaults, duration boundaries, profile overlays and
callback ordering without writes. Native repository CI runs on Linux,
macOS and Windows; these tests do not launch the Codex binary or prove native
Codex startup behavior.

The [official config reference](https://learn.chatgpt.com/docs/config-file/config-reference)
links the published schema. The [official profile documentation](https://learn.chatgpt.com/docs/config-file/config-advanced)
describes separate profile files and removal of legacy profile selection. The
release schema and runtime code were checked at the pinned commit, rather than
assuming the current public schema describes every installed version.
