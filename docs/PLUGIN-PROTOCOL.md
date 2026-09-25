# rigsmith plugin protocol

rigsmith has **one** extension mechanism, used for two kinds of plugin:

1. **Ecosystem / language adapters** (dotnet, node, go, and future cargo, python, …)
2. **Changelog generators** (default, keepachangelog, emoji, JSON-for-a-website, …)

Both are external commands invoked over a **versioned JSON-on-stdin /
result-on-stdout** contract — the same delegation model the tools already use for
`git`, `gh`, and the native package managers. This was chosen deliberately over
Go `plugin` `.so`, HashiCorp `go-plugin` (gRPC), and WASM; see the rationale in
net-changesets `docs/changelog-generator-plugins-design.md`. A plugin here is a
stateless one-shot pure function — the ideal shape for a subprocess.

## The load-bearing rule: built-ins dogfood the contract

The built-in adapters and the built-in changelog renderer are **not** a
privileged bypass. They implement the very same Go interfaces
(`plugin.Ecosystem`, `plugin.ChangelogGenerator`) that the subprocess transport
mirrors. An external plugin and a built-in are interchangeable.

Why this matters: if our own default can be driven entirely through the request
object, the contract is complete. If a golden can't be reproduced from the
request alone, the contract is missing a field — and we find out because *our own
renderer breaks*, not because a third party complains.

```
plugin.Ecosystem (interface)
├── ecosystem/dotnet  ─┐ in-process built-ins
├── ecosystem/node     │  (reference implementations)
├── ecosystem/gomod   ─┘
└── plugin.SubprocessEcosystem  ── external command over JSON

plugin.ChangelogGenerator (interface)
├── planner built-in renderer    ── in-process reference
└── plugin.SubprocessChangelogGenerator ── external command over JSON
```

## Versioning

`APIVersion` is an integer (currently `1`). The engine sends the highest version
it speaks; a plugin that doesn't recognize it must exit non-zero rather than
guess. Additive fields don't bump the version; removing/renaming/re-meaning a
field does.

## Ecosystem adapter contract

Invoked as `plugin <method>` with a JSON request on stdin and a JSON response on
stdout. Methods (see `core/plugin/protocol.go` for the exact structs):

| Method | Request | Response | Purpose |
|---|---|---|---|
| `info` | `{apiVersion}` | `EcosystemInfo{id, displayName, capabilities, manifestPatterns}` | identity + capabilities |
| `detect` | `{repoRoot}` | `{detected: bool}` | does this ecosystem apply here? |
| `discover` | `{repoRoot, sourcePath}` | `{packages: [Package]}` | enumerate releasable packages |
| `set-version` | `{package, newVersion, dependencyUpdates}` | — | stamp a version (format-preserving) |
| `publish` | `{package, packageSource, access, dryRun}` | `{published, skipped, message}` | publish via the native package manager (idempotent) |
| `published` | `{package, packageSource, auth?, user?, authUnavailable?}` | `{published, noRegistry}` | is this version already on the registry? Publishes nothing. `noRegistry` for an ecosystem released by its tag alone; a registry that can't be reached is an error, never `published: false`. `auth` (`{token, method}`) is the ecosystem's resolved publish credential and `user` its account name, for a registry that won't answer an anonymous read: send them only when the registry asks (a 401), only to its own host, over https, and never across a redirect elsewhere. `authUnavailable` means one is configured but couldn't be resolved: don't substitute an ambient credential (an environment variable) for it. Credentials written into `packageSource` itself are the source's own and still apply. Mask whichever credential is used in any error |

`Package` carries `{name, displayName, version, dir, manifestPath, versionFile,
private, dependencies[]}`. `versionFile` differs from `manifestPath` when the
version is shared (a `Directory.Build.props`, a workspace-root version) — that's
what drives lockstep grouping. `dependencies[]` carry `{name, kind, range}`; an
empty `range` (e.g. .NET ProjectReference, a Go `require`) is treated as
always-out-of-range, hence the dependent is always patch-bumped.

### Discovery resolution (for the host)

A plugin command is resolved like git subcommands:
1. a built-in id (`dotnet`, `node`, `go`) → the in-process adapter;
2. a path (`./adapters/cargo`) → executed directly;
3. a bare name → `rigsmith-ecosystem-<name>` on `$PATH`.

## Changelog generator contract

Invoked once **per package being released**, `ChangelogRequest` JSON on stdin,
rendered entry **text** on stdout (the block under the package's `# Title`,
excluding the title — the engine owns file placement and insertion). See
`core/plugin/protocol.go` `ChangelogRequest` and net-changesets'
`docs/changelog-generator-plugins-design.md` for the full field reference.

Each change carries a `type` and a `scope` when the changeset declared one —
the type is which section it belongs in, the scope is which tool. The request
also carries `scopeOrder`, the repo's configured `changelogScopes`, because a
change's own scope does not say which tool a reader should see first. A
generator that ignores both still renders correctly; the bundled
`examples/plugins/changeset-changelog-changelogen` honours them, and is the
reference for what "correctly" looks like.

What an external generator gets beyond the built-in's own inputs:

- each change's `commit` (the one that added the changeset, or a commit-sourced
  change's own), and, when the options carry a `repo` (as
  `@changesets/changelog-github`'s do), its `pr` and `author` login, looked up
  on GitHub. The `summary` is as authored; the built-in `changelog-git` and
  `changelog-github` decorate their summaries instead, as @changesets does.
- `dependencyUpdates`: the released dependencies behind the entry,
  `{name, displayName, newVersion}`, sorted as the built-in lists them, in
  place of an "Updated dependencies" change, as @changesets hands
  `getDependencyReleaseLine` its own list. The built-in renders the same
  field.
- the output is normalized to end in exactly one newline, however many the
  generator prints, so the next entry starts on a line of its own.

Options: configured as a `[name, options]` tuple, as @changesets has it
(`"changelog": ["my-generator", { "style": "terse" }]`), the options value
reaches the generator as the request's `options` field: the same JSON,
whitespace aside, for the generator to interpret, as @changesets passes it to
`getReleaseLine`. The string form (`"changelog": "my-generator"`) sends no
`options`, and neither does a `null` in the tuple. The engine reads nothing
in it, except `repo` for the built-in `@changesets/changelog-github`.

Resolution: `default` → in-process built-in; a path → executed; a bare name →
`changeset-changelog-<name>` on `$PATH`.

## Status

The protocol types, subprocess host, registry and in-process built-ins are in
place, and the built-in changelog renderer runs through `ChangelogRequest`
(`planner.RenderEntry` builds the request a subprocess would get and hands it
to the built-in generator), so it dogfoods the contract. The reference
changelog plugin is `examples/plugins/changeset-changelog-changelogen`, and
the tests drive a compiled one (`internal/changerig/cmdtest/testdata/optionsplugin`)
through the CLI. What remains: a reference external *ecosystem* plugin as a
conformance test; the tests drive the subprocess host with an in-binary
helper today (`core/plugin/published_test.go`).
