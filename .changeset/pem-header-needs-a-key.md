---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

A PEM header with no key after it no longer refuses the sync, or gets scrubbed out of the sentence it was mentioned in.

The credential tripwire matched `-----BEGIN … PRIVATE KEY-----` and stopped there, so the header alone counted as key material. Because that verdict is about the FILE — credential material, cannot be redacted — anything that merely names the header refused every sync until the file was deleted by hand.

Which is not a rare shape. A cached tool result holding a grep over a Nuxt project carried minified sourcemaps of `jose` and `@octokit/auth-app`, whose source compares against the header text: `privateKey.includes("-----BEGIN RSA PRIVATE KEY-----")`. Twelve headers, no key, sync refused. Deleting the file bought one run, because the session transcript that discussed the incident then refused the next one. Any runbook, code sample or conversation about PEM was a sync outage waiting to happen — including this project's own tests.

The scan now asks what follows the header: a line break or a space, then PEM base64, allowing for the RFC 1421 attribute lines an encrypted key puts first. Escaped line breaks count, single (`\n`) and doubled (`\\n`) — a transcript records tool output that was itself JSON, so the same break arrives both ways.

The text rule the scrubber uses gained the same requirement, and for the same reason. It used to run from the header to the end of the string unconditionally, which existed to keep the scrubber a superset of a scanner that fired on headers — so a mention of one had the rest of the sentence silently rewritten. With the scanner asking for material, that workaround was what remained of the bug.

The old rule survives, named `HasPrivateKeyHeader` so the difference cannot be misread, for the one place it is still right: raw text read a line at a time, where the body is on the lines after the header and cannot be seen, so the header alone has to be enough.

The two scrubbers were refusing on the marker for the same reason, one layer down: after the rewrite, a surviving header was read as a key the rule had failed to span. Nothing needs rewriting in a sentence that merely names one, so the header always survived and the transcript could not be scrubbed at all. Both now ask for material, which only a JSON record can reach — raw text carrying a header still refuses on the header alone, before the rewrite, because there the body is on lines the loop cannot see.
