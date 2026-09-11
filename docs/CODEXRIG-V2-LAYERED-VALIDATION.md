# Layered Codex permission validation (8b.6b)

`configcodec.ValidateConfigSetWithLayers` validates each base/profile scenario
in a caller-supplied destination context. `adapter.PrepareLayeredConfigRestore`
requires a `ConfigLayerSource` that rereads this context and still requires the
destination callback.
The versioned entry point remains a convenience wrapper with an empty context.
All of these APIs are internal; the public command remains `codexrig inspect`.

## Layer contract

`ValidationLayers.Before` and `After` each contain TOML documents in ascending
precedence order. Effective configuration is built in this order:

1. `Before` layers, then the proposed home `config.toml`.
2. The selected named profile, if any. Each profile starts from the same base.
3. `After` layers.

The default scenario and every named profile must pass. Tables merge recursively;
arrays/scalars replace. Permission selection also retains layer order: a later
`sandbox_mode` selects legacy behavior, while a later `default_permissions`
selects profile behavior. If both occur in one layer, profile selection wins.
This avoids inferring selection from a flattened map that retains both keys.

The layer source must return trusted, already-selected destination layers. This API does
not discover system or project files, decide project trust, remove disallowed
project keys, or implement session-flag special cases. Such filtering and CLI
selection must happen before these inputs are used. Context never comes from a
remote backup. It does not enter proposed restore files, application writes or
publication. Formatting and JSON serialization of `ValidationLayers` omit bytes.
Permissions and sandbox settings remain machine-local under the capture policy;
validation checks preserved destination settings without making them portable.

There may be 32 base/profile files and 16 context documents, counting a present
requirements document as one. Each input is at most 1 MiB; all inputs together are
at most 8 MiB. Existing TOML depth limits apply, with another depth check on the
complete effective configuration. Cancellation is checked between documents and
while traversing catalogs/inheritance; parsing and schema calls are not forcibly
interrupted. Diagnostics contain no private values or paths.

## Requirements and permission selection

`Requirements` is a separate, already-resolved requirements TOML document. It is
never merged as an ordinary config overlay. This step checks its permission
catalog and selection fields; other requirement fields remain outside this API's
guarantees.

- Managed profiles come from `[permissions.<name>]`. A name collision with a
  config-defined profile is refused, even when that profile is inactive.
- Colon-prefixed declarations are reserved. Native built-ins are `:read-only`,
  `:workspace` and `:danger-full-access`.
- `[allowed_permission_profiles]` values must be booleans and every referenced
  ID must exist, including entries set to false. A requirements default needs an
  allowlist and must be allowed. The native implicit default is `:workspace` only
  when both `:workspace` and `:read-only` are allowed.
- A disallowed user selection falls back to the requirements default for
  validation. This does not rewrite the user's config. An allowlist forces profile
  selection even if a later config layer selected legacy sandbox behavior.
- An active custom selection must exist and its inheritance chain must resolve
  without a cycle. Only `:read-only` and `:workspace` are extensible built-ins;
  `:danger-full-access` is selectable directly. Inactive inheritance is retained,
  matching the native catalog's ability to mark a profile unavailable.
- Requirements `[permissions.filesystem]` is a constraint entry, not a profile.
  Only `deny_read` is accepted and, when present, must be an array of strings.
  Unknown keys are refused by the restore policy rather than silently ignored.
  Deny-read path semantics and enforcement remain destination responsibilities.

## Permission map shapes

The pinned upstream schema has five `serde(flatten)` map definitions without
value constraints. A private parsed copy adds the missing `additionalProperties`
constraints for permission profiles, workspace roots, filesystem entries, network
domains and Unix sockets using the release's existing definitions. This activates
nested type/enum checks and rejects invalid profile fields. The source artifact,
its hash and its license remain unchanged. If a future schema already constrains
one of those maps, compilation refuses the policy augmentation until re-audited.

## Remaining work

This validates permission shapes, catalogs and selection, not a usable sandbox.
Filesystem path/glob compilation, network-domain normalization, inherited
filesystem/network policy compilation, MITM action/reference checks, platform
constraints and other managed requirements still need enforcement. The existing
provider/MCP checks also apply to each effective scenario.

`PrepareLayeredConfigRestore` snapshots the ordered layer bytes, including
requirements presence, and binds their fingerprint plus the source to the plan.
It rereads before and after destination validation. Every later `Check` rereads
again; `Apply` uses those checks before staging, before each replacement, after
the final replacement and for no-op plans. A source error, oversized result or
changed fingerprint refuses continuation without exposing private diagnostics.
Earlier confirmed writes remain reported if a change is discovered mid-batch.
The reader is released from the plan on close; the caller owns its handles.

Production sources must discover and pin system/project/requirements files,
detect identity and trust/selection changes, and stay live through plan closure.
A cached function is not a production source. The content rechecks do not lock
external policy writers or eliminate the interval between a check and a write.
Those writers must be coordinated through application. Production source discovery,
identity/trust checks, full destination enforcement, local dependencies and
credential readiness remain required before user-facing restore is wired. No live configuration, environment values or authentication stores are
read by this implementation, and no helper is executed.

The exact-release source references are in [schema provenance](../internal/codexrig/configcodec/schema/README.md).
Synthetic tests cover field shapes, managed conflicts/fallbacks, active and
inactive inheritance, precedence in both directions, profile independence, limits,
privacy, source changes during preparation and before apply (including no-ops),
and preparation/application without copying external context.
