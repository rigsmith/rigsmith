---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`publish-plan` now works with private NuGet feeds such as GitHub Packages, Azure Artifacts and feedz. When a feed asks for credentials, it's sent your publish credential (`dotnet.auth`, or else `NUGET_API_KEY`), and only that feed receives it, over https. If `dotnet.auth` can't be resolved, the error says so plainly, and never shows what a credential helper printed.
