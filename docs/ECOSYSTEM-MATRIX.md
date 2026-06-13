# Ecosystem parity matrix

How rig's per-ecosystem support lines up across the five ecosystem
implementations in [`core/ecosystem`](../core/ecosystem). This is the
at-a-glance companion to [FEATURE-PARITY.md](FEATURE-PARITY.md) (which tracks
parity against the source .NET/Node tools).

**Legend:** ✅ implemented · ⚠️ available but not wired up (native tool exists) ·
— not applicable

## `rig` dev-loop & package verbs

Verbs resolve through each adapter's `EcosystemInfo.DevCommands`; cross-cutting
verbs (`kill`, `doctor`, `cd`, `setup`, `self-update`) aren't ecosystem-specific
and are omitted.

| Verb | .NET | Node | Go | Cargo | Notes |
|---|:--:|:--:|:--:|:--:|---|
| build | ✅ | ✅ | ✅ | ✅ | |
| test | ✅ | ✅ | ✅ | ✅ | |
| run / dev | ✅ | ✅ | ✅ | ✅ | |
| format / fmt | ✅ | ✅ | ✅ | ✅ | |
| lint | ⚠️ | ✅ | ✅ | ✅ | go→`go vet`, cargo→clippy; .NET has no native lint verb |
| typecheck / check | ⚠️ | ✅ | ✅ | ✅ | go→`go vet`, cargo→`cargo check`; Go folds type-checking into vet |
| clean | ✅ | — | ✅ | ✅ | Node has no canonical clean (maps to a `clean` script if present) |
| rebuild / rb | ✅ | ✅ | ⚠️ | ⚠️ | clean→build seam; go/cargo not special-cased |
| install / restore | ✅ | ✅ | ✅ | ✅ | |
| ci (frozen install) | ✅ | ✅ | ✅ | ✅ | restore --locked-mode · npm ci/frozen-lockfile · go mod download · cargo fetch --locked |
| add | ✅ | ✅ | ✅ | ✅ | |
| uninstall / remove | ✅ | ✅ | ⚠️ | ✅ | Go: edit go.mod + `go mod tidy` |
| outdated / od | ✅ | ✅ | ✅ | ✅ | cargo via the `cargo-outdated` subcommand |
| upgrade | ✅ | ✅ | ✅ | ✅ | range-respecting; .NET to latest (no ranges) — see below |
| global / g | ✅ | ✅ | ✅ | ✅ | |
| dlx / x | ✅ | ✅ | ⚠️ | ⚠️ | .NET→`dnx`, node→npx/bun x/dlx |
| watch / w | ✅ | ✅ | ⚠️ | ✅ | .NET→`dotnet watch`, cargo→cargo-watch; Go has no native watch |
| coverage | ✅ | ✅ | ✅ | ✅ | cargo→`cargo llvm-cov` |
| publish (app) | ✅ | — | — | — | `dotnet publish` self-contained app packaging |

`rig upgrade` is range-respecting: a bare invocation shows the in-range plan and,
on a TTY unless `--yes`, asks one confirm before upgrading. node/cargo run their
native bulk command, go/.NET pin per-package, and cargo's plan comes from
`cargo update --dry-run`. .NET, which has no version ranges, upgrades to latest.
`rig outdated -i` is the to-latest selective picker.

## Release / version engine (`relrig` + `changerig`)

Driven by the shared adapter interface in
[`core/plugin/ecosystem.go`](../core/plugin/ecosystem.go).

| Capability | .NET | Node | Go | Cargo | Regex |
|---|:--:|:--:|:--:|:--:|:--:|
| Discover packages | ✅ | ✅ | ✅ | ✅ | ✅ |
| Read version | ✅ | ✅ | ✅ | ✅ | ✅ |
| Write version | ✅ | ✅ | ✅ | ✅ | ✅ |
| Publish to registry | ✅ | ✅ | — | ✅ | — | 
| Range-aware cascade | ⚠️ | ✅ | ✅ | ✅ | — |
| Git tagging | ✅ | ✅ | ✅ | ✅ | ✅ |

Publish — NuGet / npm / crates.io; Go and Regex release by git tag (no registry
push). Range-aware cascade — .NET `ProjectReference` carries no version range, so
dependents always cascade rather than gating on whether the bump stays in range.

## Remaining gaps (⚠️ above)

- **.NET** `lint` / `typecheck` — no native SDK verb (would need an external
  analyzer/tool).
- **Go** `uninstall`, `dlx`, `watch`, and the `rebuild` seam — no native single
  command; would wrap `go mod tidy`, `go run pkg@latest`, an external watcher.
- **Cargo** `dlx` and the `rebuild` seam.
- **Node** `clean` — npm has no canonical clean (maps to a project `clean`
  script when defined).
