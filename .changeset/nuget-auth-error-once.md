---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`publish-plan`'s error for a private NuGet feed that asks for credentials when `dotnet.auth` can't be resolved says so once, with the reference and why, and that `NUGET_API_KEY` isn't used in its place. The reason never includes what resolving the reference printed: a failed `cmd:` helper's output could carry a secret, so the reference is named by kind (`cmd:…`) and the error says it failed. Before, it said the configured `dotnet.auth` couldn't be resolved twice over. The docs now state the transport rule as the code applies it: credentials go over https, or plain http to a loopback host for a local test feed.
