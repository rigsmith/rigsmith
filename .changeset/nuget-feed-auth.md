---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`publish-plan` can check a private NuGet feed. When the feed answers 401, the version check asks again with the publish credential as HTTP Basic auth: credentials in the source URL, else the resolved `dotnet.auth`, else `NUGET_API_KEY`, with `dotnet.user` (or a placeholder) as the account name. That's how GitHub Packages, Azure Artifacts and feedz take a token. Credentials are only ever sent to the source's own host, over https or plain http to a loopback host for a local test feed (a redirect elsewhere is refused), never to nuget.org, and never before the feed asks. A `dotnet.auth` the plan job can't resolve sends none, `NUGET_API_KEY` doesn't stand in for it, and the error names it if the feed then asks. Before, an authenticated feed answered 401 and the plan failed.
