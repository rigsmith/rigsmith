---
type: fix
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

Two Claude Desktop accounts whose ids begin the same way no longer merge into one folder in the Places view.

The group was keyed by the NAME shown for an account, and with no email on file that name was the account id cut at its first `-`. Two accounts sharing that prefix therefore shared a key: one group, holding both accounts' sessions, carrying one account's id. Clicking it showed only that one account's sessions, because the drawer filters on the id — so the row advertised a count it could not produce, which is the exact failure the split exists to prevent. Groups are now keyed by the account id, and an account with no email on file is named by its id in full: long, but never two accounts under one heading.

A Desktop sidecar that will not parse also keeps its filename as its label. It used to be relabelled `(untitled) <first segment of the filename>`, which presented a slice of a filename as though it were a session id — for `local_broken.json`, a session called "broken". The relabelling is right for a record that parsed and simply had no title; for one we could not read at all, the filename is the only fact there is, and it is also what somebody needs in order to go and look at the file.