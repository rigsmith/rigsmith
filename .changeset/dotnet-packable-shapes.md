---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

two shapes of .NET project that pack were not discovered. When a shared `Directory.Build.props` set `<IsPackable>false</IsPackable>` for everything and a later `PropertyGroup` under a `Condition` set it `true` again, the first value was taken as the last word, so every project beneath it was left out. Discovery now reads every `IsPackable` in the project and its ancestor props files: the last unconditional value wins, as in MSBuild, and a conditional `true` anywhere makes the project packable since conditions are not evaluated; a commented-out element counts for nothing. And MinVer declared once as a `GlobalPackageReference` in the nearest `Directory.Packages.props` (an outer one is read only where the nearer file imports it, as restore does), with no csproj naming it, now counts as a build-time version, so such a project is discovered rather than skipped.
