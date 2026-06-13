# polyglot test fixture

A self-contained multi-ecosystem workspace for exercising `rig` — most usefully
the **workspace-aware `rig doctor`**, which discovers every project and reports,
per ecosystem, the toolchain plus a row per project with its own state:

```sh
cd examples/polyglot
rig doctor
```

```
Go
  ✓ go      go1.26.x
  ✓ a       go 1.26
  ✓ b       go 1.23
Node
  ✓ node    v24.x
  ✓ npm     npm 11.x
  ! app     deps declared, not installed — run `rig install`
  ✓ lib     no dependencies
.NET
  ✓ dotnet  SDK 10.0.x (satisfies global.json pin 10.0.100)
  ✓ Net8    net8.0
  ✓ Net10   net10.0
Cargo
  ✓ cargo   1.8x
  ✓ crate-a v0.1.0
  ✓ crate-b v2.0.0
```

## Layout (deliberately varied, to show every state)

- **`.rig.json`** — marks this dir as its own rig root, so `rig` here resolves to
  the fixture, not the parent repo.
- **`go/mod-a`, `go/mod-b`** — two modules on different `go` directives (1.26 / 1.23).
- **`node/app`** — declares a dependency but isn't installed → the `deps` warning.
  Run `npm install` here to flip it to "deps installed".
- **`node/lib`** — no dependencies → quiet `✓`.
- **`dotnet/Net8`** (a runnable console) and **`dotnet/Net10`** — different target
  frameworks. Their `<Version>` comes from **`dotnet/Directory.Build.props`**, not
  inline — the common real-world pattern, which discovery resolves from the
  ancestor props file.
- **`global.json`** — pins an SDK so `doctor` shows the pin-satisfaction note.
- **`rust/crate-a`, `rust/crate-b`** — two crates on different versions.

Nothing here needs to build for `doctor`; `dotnet/Net8` is runnable so
`rig run` (→ the workspace picker) has a real target.
