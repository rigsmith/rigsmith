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

## Network hook action validation

After layer composition, MITM action definitions are checked in every config and
managed permission profile, including inactive profiles and disabled networks.
As in native `NetworkMitmToml` deserialization, each action must contain at least
one strip-header or inject-header operation, and each hook must have a nonempty
`action` list. Unknown action fields do not count as operations. This check runs
before inheritance: a child cannot repair an empty parent action definition.
Schema checks continue to establish the required hook fields and value types.

For the selected custom permission profile, action names and hook action lists
are resolved from ancestors to child. Ancestor actions remain available, child
hooks replace the same hook's action list, and distinct ancestor hooks remain.
Child actions can satisfy references in inherited hooks. Sibling profiles cannot
supply actions. The two extensible built-ins supply no MITM actions or hooks.
This resolves the action/reference and header-declaration portion; it does not construct a runtime
network policy or change the input bytes.

Restore policy refuses an unresolved reference in the selected profile, even
when its network is disabled. This is deliberately stricter than the pinned
runtime's `selected_actions`, which skips missing names. It uses the invariant
expressed by upstream `validate_action_references`, without claiming that every
native load path invokes that function. Inactive profiles may retain unresolved
references, matching the existing inactive-inheritance boundary. Managed fallback
selection and every separate config-profile scenario receive the same checks.
Diagnostics omit action names and all private configuration values.

## Selected network hook headers and source declarations

Effective hooks in the selected permission profile now check their header-match
keys and referenced actions' strip/inject header names. Names must be nonempty
ASCII HTTP tokens of at most 65,535 bytes, matching the pinned header parser;
whitespace, separators and Unicode letters are rejected without trimming.
Header-match value declarations and glob syntax are checked as described below;
runtime matchers are not compiled. Empty header
value lists are retained, as allowed by native header-constraint validation.

Each referenced injected-header declaration must name exactly one secret source:
a nonblank `secret_env_var` or a nonblank absolute `secret_file`. Omission of the
header name defaults to empty and is rejected. Either source containing a NUL is
refused by restore policy, including an environment-variable name. Absolute-path checks use the destination host's Go
path syntax; they are lexical and do not expand `~`, resolve links, normalize
paths, or inspect files. These entry points therefore validate a local restore,
not the path syntax of an arbitrary other target OS. A missing absolute file and
an unavailable environment variable can still pass this declaration check.

Only actions referenced by effective selected hooks receive these checks.
Inactive profiles and unused actions can retain invalid header/source content;
the existing nonempty action-definition checks still apply to every declaration.
Selected profiles are checked even with networking disabled, as an explicit
restore policy. Action definitions override by name during permission-profile
inheritance: both native operation vectors default to empty when omitted, so a
child declaring only strip operations removes inherited inject operations.
Ordinary config-layer overlays still recursively combine the raw declarations
before those defaults are applied. Hook header maps merge recursively; a child
empty map cannot remove an invalid ancestor header key.

No environment values or secret files are read and no headers are produced.
Prefix/secret value validity, source availability, runtime hook matching and actual
proxy policy remain destination-readiness checks. The code applies checks once
per referenced action and preserves the original private input bytes.

## Selected matcher declarations and glob syntax

The selected inheritance chain now composes each hook's matcher declarations.
Required host/method/path/action fields replace those of the same ancestor hook;
query and header maps merge by key, with each child value list replacing the same
ancestor list. An omitted or empty child map cannot remove an ancestor key.
Optional body declarations survive inheritance and are rejected because this
Codex release does not support body matchers. Inputs remain private and unchanged.

Host checks follow native normalization for the two relevant tests: trim outer
whitespace, remove brackets or a single port suffix, and strip trailing dots,
then reject an empty host or a wildcard. This is not DNS/URL validation. Methods
must have at least one entry and every entry must be nonblank; like native code,
this does not impose an additional HTTP method token grammar. Paths must have at
least one entry. Query names must be nonempty and each query must list at least
one value. Empty header value lists remain valid.

`literal:` removes that prefix and treats everything after it literally.
`pattern:` selects glob syntax and must have nonempty contents. Unprefixed values
are literals even when they contain glob metacharacters. Path literals cannot be
empty; query/header literals can. The syntax checker follows pinned globset
0.4.18 with backslash escaping enabled and unclosed classes rejected: nested
alternates, escapes, classes and ascending ranges are checked iteratively.
Recursive-star separator consumption follows the host platform, including the
upstream Windows backslash behavior. No glob is evaluated and no native regex is
compiled; regex-engine limits and actual matching semantics remain outside this
syntax guarantee. Both path and value patterns use the same syntax checks;
their different slash-matching behavior belongs to runtime compilation.

These checks apply to effective selected hooks, including a selected disabled
network under the existing restore policy. Inactive profiles retain malformed
matchers until selected; native definition checks still apply globally.

## Remaining work

This validates permission shapes, catalogs and selection, not a usable sandbox.
Filesystem path/glob compilation, network-domain normalization, inherited
filesystem/network policy compilation, native MITM regex compilation/matching and secret-source
availability/value checks, platform
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
and preparation/application without copying external context. Network-action tests
cover native definition checks, inherited and managed references, child hook
replacement, sibling isolation, independent config profiles and refusal before
the destination callback without writes or private diagnostics.

Header regressions cover ASCII/length limits, conflicting/missing sources,
platform-local path syntax, nonexistent absolute files, inherited action defaults
versus raw layer overlays, retained header keys, unused actions, managed fallback,
cancellation and refusal before destination validation without writes or private
diagnostics.

Matcher tests cover literal versus pattern prefixes, class/range/alternate syntax,
host/method/body rules, query/header empty-value differences, inherited map keys,
child overrides, inactive profiles and private refusal before restore writes.
A local differential check compared 12,000 deterministic generated patterns
against globset 0.4.18 `GlobBuilder::build` with the same options; all agreed on
macOS. That syntax comparison did not invoke Codex, compile runtime regexes,
read real configuration or contact any configured endpoint. Permanent tests
also cover Windows-specific recursive-star separator handling.
