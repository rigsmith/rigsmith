# Codex TOML codec (8b.1)

This is the internal configuration codec for the next CodexRig capture/restore
adapter. `configcodec.Capture` and `configcodec.Restore` accept bytes and return
bytes; they do not access source files, invoke Codex, execute helpers, create
state or publish a backup. `codexrig inspect` remains the only preview command.
The codec handles both base config and named profile TOML without combining them.

## Capture policy

A structured parse precedes every transform. There is no raw-file fallback.
TOML scalar types, quoted keys, nested tables, arrays and unknown non-secret
fields without unclassified local references survive re-encoding. Comments and original formatting do not. Each input
and output is limited to 1 MiB; traversed documents have a 64-level nesting limit.
Parser/encoder errors are replaced with fixed diagnostics so source excerpts,
credential-bearing keys and values cannot enter logs.

These fields are omitted rather than replaced with a sentinel:

- Credential keys, including compound names ending in token, secret, password,
  API key, private/access/signing key, bearer or token cache. Matching ignores
  case and common separators.
- Entire `env`, `headers`, `http_headers`, `query_params`, `auth`, `oauth` and
  `credentials` values, regardless of their TOML type. This covers short and
  numeric credentials that a signature detector cannot recognize.
- `command`, `args`, `http_headers_helper`, `notify` and `hooks` values. Arbitrary
  shell/helper text can contain credentials; structured hook portability is a
  separate remaining gate. Commands are never executed by this codec.
- Top-level project trust records, `sqlite_home`, `log_dir`, `codex_home` and
  credential-store choices. These belong to the destination machine.

Documented environment references (`env_key`, `bearer_token_env_var`, `env_vars`
and MCP/provider `env_http_headers`) remain portable. Values of the latter must
be environment variable identifiers; a header named `Authorization` in that
specific reference map is not a literal credential. This exemption does not
apply to similarly named unknown sections.

After structural omission, decoded keys and string values are checked with
shared credential signatures and the conservative whole-value entropy detector.
Embedded signatures and TOML Unicode escapes cannot avoid that check. Suspect
content refuses capture with no partial output. URL userinfo and query/fragment
payloads are refused, including URLs in prose: these can carry short credentials.
This intentionally also refuses some harmless query parameters. Unknown short
secrets under innocuous names cannot be identified reliably; this codec is not a
proof that arbitrary configuration is secret-free. Publication still requires
the final backup audit. The [path policy](CODEXRIG-V2-CONFIG-PATHS.md) also
omits machine-local settings and rejects explicit unclassified local references.

An array with any protected descendant is omitted as a whole. Retaining its
public elements would lose the identity needed to restore private elements.

## Restore policy

Incoming TOML must already satisfy capture policy. Protected fields or suspect
content in an incoming backup cause a refusal, not silent credential import.
Malformed local TOML also refuses restore; it is never treated as empty.

A valid backup overlays local public values while preserving local-only fields
and unknown local additions. Missing local values remain missing: no placeholder,
source credential or fabricated null is written on a fresh machine. Arrays with
local protected descendants stay intact; credentials are never matched by array
index. A type change cannot erase a protected local subtree.

A local named MCP server, model-provider or agent entry containing any protected value
stays intact as a whole, even if the backup changes its public settings. This
prevents restoring a different endpoint/command alongside existing credentials.
It also means public changes to such an entry require manual reconciliation.
Credential-free entries can receive public changes normally. This is a deliberate
conservative merge contract, not an attempt to infer integration identity.

The resulting bytes are not automatically a runnable Codex config. In particular,
a new machine may still need a command, helper or credentials supplied locally.
The [restore planner](CODEXRIG-V2-CONFIG-RESTORE.md) merges the full destination
set and always checks file safety; a caller-supplied validator is optional.
Codex owns runtime semantics. [File application](CODEXRIG-V2-CONFIG-APPLY.md)
handles staged replacement and partial-outcome reporting. It must never publish the
local merged output, which contains destination secrets.

## Shared mechanics and Claude compatibility

`internal/agentrig/secrets` owns the existing credential signature, entropy and
prose-exclusion mechanics. Claude's JSON policy, placeholder, text rewriting,
streaming scanner and diagnostics remain in `internal/clauderig/redact`, calling
those same mechanics. Codex owns its TOML field policy and restore semantics.
Neither vendor imports the other. No scanner rule or Claude policy changes in
this extraction; the existing Claude behavior tests and pinned compatibility
suite remain the regression check.

The already-pinned `go-toml/v2` dependency becomes direct without a version change.
Tests use synthetic bytes only, including round trips, redaction/refusal, quoted
and escaped keys, native TOML types, array reordering, endpoint changes, limits,
malformed local input and fuzzed capture/restore idempotence.

## Remaining workflow

[Capture](CODEXRIG-V2-CONFIG-CAPTURE.md), [path policy](CODEXRIG-V2-CONFIG-PATHS.md),
[restore preparation](CODEXRIG-V2-CONFIG-RESTORE.md) and
[file application](CODEXRIG-V2-CONFIG-APPLY.md) are implemented internally.
Next, connect independent settings/repository wiring and public config commands,
including the minimum-version check and interrupted-restore recovery. Customizations,
sessions and hooks are later extensions. The [delivery plan](CLAUDERIG-SHARED-LAYERS-ROADMAP.md)
defines release scope; no runtime-semantic validation stage remains.

Codex field semantics were checked against the official
[configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference)
and [advanced configuration](https://learn.chatgpt.com/docs/config-file/config-advanced)
on September 11, 2026. The latter documents command-backed provider authentication
and separate named profile files. No live user config or credentials were read.
