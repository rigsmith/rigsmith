---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig publish` and `shiprig tag` speak @changesets v3's output contract. With `--output <file>` or `$CHANGESETS_OUTPUT` set, each appends a `{"type":"git-tag","tag":…,"packageName":…}` line per tag it creates, skips a tag already present locally or on the remote, and leaves pushing to the caller, as `changeset publish` does. A release action can then push exactly those tags and create a release for each.