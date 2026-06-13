# polyglot test fixture

A single directory carrying **all four ecosystem manifests** at one root —
`go.mod` (Go), `package.json` (Node), `Cargo.toml` (Cargo), `Poly.csproj`
(.NET) — so `rig` detects every ecosystem here at once.

It exists to exercise rig's cross-ecosystem behavior — most usefully the live
`rig doctor` checklist, which spins a per-ecosystem probe (`go version`,
`node --version`, `dotnet --version`, `cargo --version`) and fills in ✓/!/✗ as
each resolves:

```sh
cd examples/polyglot
rig doctor
```

It is intentionally **not buildable** — there's no source, just the manifests
needed for detection. The `go.mod` is deliberately left out of the workspace
`go.work`, so the repo's Go tooling ignores it.
