---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Preserve backup bytes through Git clone, history and restore even when Git text conversion is enabled.

Backup repositories now carry attributes disabling line-ending, encoding, keyword and clean/smudge conversions. Sync refreshes older indexes when installing these rules, and publication refuses overriding attributes that could rewrite scanned bytes. This protects both native transcripts and content-addressed chunks. Already altered historical blobs are not rewritten or silently accepted; recover those from an intact source or verified backup.
