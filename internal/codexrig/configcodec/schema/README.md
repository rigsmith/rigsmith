# Pinned Codex config schema

`codex-0.144.6.json` is an unmodified copy of `codex-rs/core/config.schema.json`
from OpenAI Codex release `rust-v0.144.6`:

- Commit: `5d1fbf26c43abc65a203928b2e31561cb039e06d`
- Source: https://github.com/openai/codex/blob/5d1fbf26c43abc65a203928b2e31561cb039e06d/codex-rs/core/config.schema.json
- SHA-256: `841e0ab1c1bd2fea736ba2d46212ab5bedc06dce9fd83bbafbf50b57b9056d17`
- Retrieved: 2026-09-11
- License and upstream notice: accompanying `LICENSE` and `NOTICE`.

The public schema endpoint changes independently of releases. Updates must pin
and audit a specific release, update the hash/version tests, and review numeric
formats and runtime constraints. Do not refresh this file during a restore.
All references in this snapshot are local; the compiler has no URL/file loaders.
Git attributes preserve the JSON bytes on Windows rather than converting LF to
CRLF, so the embedded schema has the same provenance hash on every platform.

The embedded artifact is not rewritten. A private parsed copy adds the five
permission-map value constraints described below; code supplies runtime checks.
Separate code rejects legacy profile selectors/tables and checks selected provider
references. Schema success does not prove runtime readiness: model-dependent
values, permission compilation and destination constraints still need checks.

The built-in provider IDs come from [`built_in_model_providers` in the same
release](https://github.com/openai/codex/blob/5d1fbf26c43abc65a203928b2e31561cb039e06d/codex-rs/model-provider-info/src/lib.rs#L430).
Before catalog construction, [`validate_model_providers` and
`validate_reserved_model_provider_ids`](https://github.com/openai/codex/blob/5d1fbf26c43abc65a203928b2e31561cb039e06d/codex-rs/config/src/config_toml.rs#L895-L940)
reject declarations using `openai`, `ollama` or `lmstudio`, even if unselected.
They also require nonblank custom names, reserve AWS configuration for Bedrock,
and invoke provider auth validation. The merge function's `or_insert` behavior
does **not** make colliding declarations valid: the earlier validation wins.
This corrects the initial 8b.6a documentation and tests.

[`ModelProviderInfo::validate` and `merge_configured_model_providers`](https://github.com/openai/codex/blob/5d1fbf26c43abc65a203928b2e31561cb039e06d/codex-rs/model-provider-info/src/lib.rs)
supply the auth conflicts and Bedrock override rules. Bedrock allows
`aws.profile`/`aws.region`, but all other fields must equal the native struct's
defaults. Option fields containing empty strings/maps or zero are still present
and therefore non-default.

[`RawMcpServerConfig` conversion and `McpServerEnvVar::validate_source`](https://github.com/openai/codex/blob/5d1fbf26c43abc65a203928b2e31561cb039e06d/codex-rs/config/src/mcp_types.rs)
supply the transport-field restrictions, environment-source values and duration
rules. These are deserialization checks and apply to disabled servers as well.
The Go implementation uses Rust's seconds range, not Go's nanosecond duration
range. It does not resolve or run a configured command.

Permission rules are derived from [`permissions_toml.rs`](https://github.com/openai/codex/blob/5d1fbf26c43abc65a203928b2e31561cb039e06d/codex-rs/config/src/permissions_toml.rs),
[`core/config/permissions.rs`](https://github.com/openai/codex/blob/5d1fbf26c43abc65a203928b2e31561cb039e06d/codex-rs/core/src/config/permissions.rs),
and the selection/managed-catalog functions in [`core/config/mod.rs`](https://github.com/openai/codex/blob/5d1fbf26c43abc65a203928b2e31561cb039e06d/codex-rs/core/src/config/mod.rs).
The parsed schema supplements `PermissionsToml`, `WorkspaceRootsToml`,
`FilesystemPermissionsToml`, `NetworkDomainPermissionsToml` and
`NetworkUnixSocketPermissionsToml`, whose flattened maps otherwise lack value
constraints. Existing named field constraints and all upstream bytes are retained.
Requirements handling follows [`config_requirements.rs`](https://github.com/openai/codex/blob/5d1fbf26c43abc65a203928b2e31561cb039e06d/codex-rs/config/src/config_requirements.rs).
These checks do not compile filesystem/network policy or enforce other managed
requirements; see [the layer contract](../../../../docs/CODEXRIG-V2-LAYERED-VALIDATION.md).

Network action definitions follow `NetworkMitmToml::deserialize` and
`validate_action_definitions` in the pinned `permissions_toml.rs` above.
Selected action references use ancestor-to-child hook replacement and merged
name visibility. The restore policy refuses unresolved selected references,
including disabled networks: upstream `validate_action_references` describes
that invariant, while `selected_actions` silently skips missing names. This
stricter restore policy is documented explicitly rather than presented as a
universal native startup rejection. MITM matcher/header validation and secret
source readiness remain separate gates; this code does not read those sources.
