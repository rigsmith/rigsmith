---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

two shapes of .NET project that pack but were not discovered. A shared `Directory.Build.props` that sets `<IsPackable>false</IsPackable>` for everything and then `true` again in a later `PropertyGroup` under a `Condition` ("only projects under `src/`") had its first `IsPackable` taken as the last word, so every project beneath it was left out; every `IsPackable` in the project and its ancestor props files is now read, and any `true` makes the project packable — conditions are not evaluated, and a listed package with no version costs less than a real one silently missing. An explicit `false` with no `true` anywhere still excludes. And under Central Package Management MinVer is declared once, as `<GlobalPackageReference Include="MinVer" … />` in `Directory.Packages.props`, with no csproj naming it; discovery only looked for a `PackageReference` in the project, so such a project was taken as unversioned rather than versioned at build time. A `GlobalPackageReference` in the nearest `Directory.Packages.props` now counts — the nearest only, since restore reads no other unless that file imports the one above it, which is followed too. A commented-out `IsPackable` or MinVer reference counts for nothing, either way.
