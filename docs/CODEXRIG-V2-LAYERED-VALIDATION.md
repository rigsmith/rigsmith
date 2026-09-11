# Layered Codex permission validation (8b.6b)

`configcodec.ValidateConfigSetWithLayers` validates each base/profile scenario
in a caller-supplied destination context. `adapter.PrepareLayeredConfigRestore`
uses this context during preparation and still requires the destination callback.
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

The caller must supply trusted, already-selected destination layers. This API does
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
  Its basic field types are checked, but deny-read path semantics and enforcement
  remain destination responsibilities.

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

Production discovery must pin and recheck system/project/requirements sources,
then check local dependencies and credential readiness without executing helpers.
`ConfigRestorePlan.Check` currently covers home files only; it does not pin these
caller-supplied external sources. `PrepareLayeredConfigRestore` must not be wired
to a user-facing restore command until that freshness and destination work is
complete. No live configuration, environment values or authentication stores are
read by this implementation, and no helper is executed.

The exact-release source references are in [schema provenance](../internal/codexrig/configcodec/schema/README.md).
Synthetic tests cover field shapes, managed conflicts/fallbacks, active and
inactive inheritance, precedence in both directions, profile independence, limits,
privacy and preparation/application without copying external context.
