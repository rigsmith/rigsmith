# The plugin protocol

rigsmith has **one** extension mechanism, used for two kinds of plugin:

1. **Ecosystem / language adapters** — dotnet, node, go, cargo, and future
   python, …
2. **Changelog generators** — default, keepachangelog, emoji, JSON-for-a-website, …

Both are external commands invoked over a **versioned JSON-on-stdin /
result-on-stdout** contract — the same delegation model the tools already use
for `git`, `gh`, and the native package managers. A plugin is a stateless
one-shot pure function — the ideal shape for a subprocess. (This was chosen
deliberately over Go `plugin` `.so`, HashiCorp `go-plugin` (gRPC), and WASM.)

## The load-bearing rule: built-ins dogfood the contract

The built-in adapters and the built-in changelog renderer are **not** a
privileged bypass. They implement the very same Go interfaces
(`plugin.Ecosystem`, `plugin.ChangelogGenerator`) that the subprocess transport
mirrors. An external plugin and a built-in are interchangeable.

Why this matters: if our own default can be driven entirely through the request
object, the contract is complete. If a golden can't be reproduced from the
request alone, the contract is missing a field — and we find out because *our
own renderer breaks*, not because a third party complains.

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
guess. Additive fields don't bump the version; removing, renaming, or
re-meaning a field does.

## Ecosystem adapter contract

Invoked as `plugin <method>` with a JSON request on stdin and a JSON response on
stdout:

| Method | Request | Response | Purpose |
|---|---|---|---|
| `info` | `{apiVersion}` | `EcosystemInfo{id, displayName, capabilities, manifestPatterns}` | identity + capabilities |
| `detect` | `{repoRoot}` | `{detected: bool}` | does this ecosystem apply here? |
| `discover` | `{repoRoot, sourcePath}` | `{packages: [Package]}` | enumerate releasable packages |
| `set-version` | `{package, newVersion, dependencyUpdates}` | — | stamp a version (format-preserving) |
| `publish` | `{package, packageSource, access, dryRun}` | `{published, skipped, message}` | publish via the native package manager (idempotent) |

::: tip Full reference
The complete protocol — every struct, the changelog-generator contract, and the
reference Node plugin — is in
[docs/PLUGIN-PROTOCOL.md](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/PLUGIN-PROTOCOL.md).
:::
