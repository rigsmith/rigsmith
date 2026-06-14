# rigsmith demo monorepo

A small polyglot monorepo used to exercise the rigsmith tools (`shiprig`, `rig`) by hand.
This is illustrative sample data, not production code.

## Layout

```
demo/
  dotnet/                 .NET (csproj)
    Demo.Core/            Version 1.0.0, PackageId Demo.Core
    Demo.App/             Version 2.0.0 -> ProjectReference to Demo.Core
  node/                   pnpm workspace
    packages/core/        @demo/core 1.0.0
    packages/app/         @demo/app 2.0.0 -> depends on @demo/core
  go/                     two standalone modules (intentionally NOT in a go.work)
    core/                 module demo.example/core
    app/                  module demo.example/app -> requires demo.example/core
  rust/                   cargo workspace
    core/                 demo-core 1.0.0
    app/                  demo-app 2.0.0 -> path dep on demo-core
  .changeset/             config + one example changeset (Demo.Core minor)
```

Each ecosystem has an `app` that depends on its `core`, so a bump to `core`
cascades a release to `app`. The example changeset bumps `Demo.Core` (minor),
which should cascade a patch to `Demo.App`.

## Try it

```
# from examples/demo:
shiprig status --verbose
shiprig version --dry-run
rig build      # picks an ecosystem based on cwd
```

Because all four ecosystems coexist at the demo root, `shiprig info` will show
packages from each ecosystem at once. You can also `cd` into a single-ecosystem
subdir (e.g. `cd rust`) to scope the tools to just that ecosystem.
