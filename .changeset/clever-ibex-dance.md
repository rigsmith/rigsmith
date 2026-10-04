---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

`changerig add` no longer overwrites an existing changeset. The random name it picks for a new changeset could already belong to a pending one, which was silently replaced while the command reported "Created"; it now picks another name instead.