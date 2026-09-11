# Codex config path policy (8b.3)

The internal TOML codec now applies path policy during both base and profile
capture. Restore verifies incoming backups against the same policy. This does
not add a command: `codexrig inspect` remains the only preview workflow.

## Machine-local settings

The codec omits these settings, irrespective of value type or path spelling:

| Settings | Capture/restore decision |
| --- | --- |
| `permissions`, `default_permissions`, `sandbox_mode`, `sandbox_workspace_write` | Keep the permission definitions and selection local as complete units, including grants, denials, workspace roots and sockets. |
| `model_instructions_file`, `experimental_compact_prompt_file`, `model_catalog_json`, `agents.<name>.config_file` | Do not copy references to files that the adapter has not captured. |
| `mcp_servers.<name>.cwd` | Keep process working directories local. Provider `auth` helpers were already omitted as a unit. |
| `skills.config`, `desktop.custom_file_handlers`, `marketplaces`, `plugins` | Keep artifact selections local until structured customization capture exists. This also omits remote marketplace declarations for now. |
| `otel`, `features.network_proxy` | Preserve coupled routing, certificate, socket and policy settings as units. |
| `shell_environment_policy.set` | Keep explicit environment values local, including short credentials and relative paths. Variable-name references remain portable. |

Existing omissions still cover project trust, home/log/database locations,
credentials, commands, arguments and hooks. Relative paths in the fields above
are local too: resolving them under a different configuration directory could
select a different file. There is no home substitution, environment expansion,
path mapping, filesystem probing, helper execution, or referenced-file capture.

These are CodexRig's conservative portability decisions, based on field semantics
in the official [Codex configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference),
checked September 11, 2026. They do not describe restrictions imposed by Codex.

## Unclassified references

Outside omitted fields, decoded TOML keys and string values are checked for
explicit local references. If a reference matches, capture returns
`ErrUnclassifiedLocalReference` with no partial output. The diagnostic contains
no key, source path, or value.

The same platform-independent checks run on every OS. They recognize rooted
POSIX and Windows paths, drive-relative Windows paths, UNC/device paths, explicit
`./` and `../` references, tilde paths, file URIs and shell environment references
such as `$HOME`, `${ROOT}`, `$env:USERPROFILE` and `%USERPROFILE%`. Path syntax at
recognized token boundaries in prose is checked too. TOML Unicode escapes are
decoded first. Normal URL paths, including literal environment-variable text,
are allowed after the existing credential check. Adjacent local suffixes such as
`https://example.com,/source/path` remain subject to path refusal. Ambiguous
URL punctuation followed by local syntax is treated conservatively as a local
suffix. URL userinfo, queries, fragments and malformed escapes still refuse capture.

This is a conservative tripwire, not a parser for arbitrary prose or new Codex
schemas. It intentionally leaves ambiguous bare relative strings such as
`repo/label` alone: those can also be model IDs, tool names or ordinary text.
Known path fields are handled structurally even for bare filenames. An unknown
field holding a bare filename, unusual interpolation or an unrecognized embedded
path still needs a future schema-specific rule. Unknown public fields otherwise
retain their TOML types. Final publication audit and usable-config validation
remain required; successful capture alone does not authorize publication.

## Restore and validation

Incoming local-only settings are rejected with `ErrUnsafeBackup`; unclassified
references are rejected with `ErrUnclassifiedLocalReference`. Destination local
values remain intact.
An MCP server, model provider or named agent with protected local descendants
stays whole, so public backup values cannot rebind a destination command, role
file or credential. Local arrays containing protected values likewise remain
whole across reordering. A type change cannot erase a protected subtree, including
unknown local paths. Fresh machines receive no fabricated path or placeholder.

The policy is applied automatically by `adapter.CaptureConfig` to every selected
file. A refusal in one profile discards the entire batch. Synthetic tests cover
these boundaries and verify that source bytes remain unchanged. No live user
configuration, credentials, or referenced files are read in validation.

[Restore preparation](CODEXRIG-V2-CONFIG-RESTORE.md) now checks the complete
destination set around a required validation callback. [File application](CODEXRIG-V2-CONFIG-APPLY.md)
now handles replacement. Supported-version validation, structured hooks/customizations,
independent CodexRig state/repository and sync/restore commands remain the next
8b work. The shared path mapping engine and Claude adapter are unchanged; this
policy concerns Codex-specific TOML semantics.
