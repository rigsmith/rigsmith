---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

`@changesets/changelog-github` now works as it does in @changesets 1.0:

- A changeset can name its pull request, commit or authors on their own lines: `pr: #12`, `commit: abc1234`, `author: @someone` (one line per author). They come out of the changelog text and replace what rig would otherwise look up. These lines no longer count as a `type:` prefix.
- The `template` option sets the layout of each line, using `{summary}`, `{ref}`, `{pull}`, `{commit}` and `{authors}`, for example `"\n\n- {summary} {ref}"`. rig adds the bullet itself, so a template that starts with one isn't doubled. An unknown token stops `version` with an error.
- A bare `#123` in a summary becomes a link to that issue.
