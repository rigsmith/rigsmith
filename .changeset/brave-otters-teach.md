---
type: docs
"github.com/rigsmith/rigsmith"
---

The stack workspace guide is rewritten around a single layout: everything goes in the workspace, your own project included. The page previously offered two arrangements and left you to pick, which sounds accommodating and is not — the arrangement where your app stays outside gives up the thing a workspace exists for, since a change spanning your app and a library is two commits in two repos again, and it quietly needs a second build file that most people do not discover until something has been building against a published package for a week. One tree, one overlay, one commit.

The page now covers getting work back out both ways: `push` fast-forwards a project you own with its history intact, and `send` proposes a squashed branch to a fork you contribute to. Along with them, the things that cost the most time to find out: a member carrying its own root build file silently hides the overlay from everything beneath it, a swap can be verified by asking MSBuild what it evaluated rather than trusting a green build, conditions written against `Filename` match the wrong package because MSBuild splits at the last dot, and swapping a package for a project reference breaks any publicizer reaching that dependency's internals. Private upstreams and pinning a library to an older release are documented where they come up rather than as caveats at the end.
