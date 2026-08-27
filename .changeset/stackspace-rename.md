---
type: docs
scope: rig
"github.com/rigsmith/rigsmith"
---

The thing `rig stack` builds is now called a **stackspace** rather than a workspace, everywhere: the guide, the configuration reference, both READMEs, the JSON schema descriptions and the messages rig prints. Nothing about the commands or the manifest changes — `rig stack init`, `rig.stack.jsonc` and every key in it are untouched.

The old name was overloaded twice over. Inside rig, `workspace` already means intra-repo package discovery, which is what the release engine walks; the design notes had recorded that when the verb was named and the prose then went and used it for the fused repo anyway. Outside rig, a reader arrives having already spent the word on npm workspaces, `go.work` or their editor. A coined word costs a definition the first time you meet it and buys one that means exactly one thing here and nothing anywhere else.

If you wrote the .NET build overlay by hand, its escape-hatch property is now `UseStackSources`, matching the `StackSource` items beside it; rename it in your own file when convenient, or leave it and lose only the ability to force a package-based build with a flag.
