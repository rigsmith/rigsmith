---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack status` now tells you when a fused project holds work that exists nowhere else. A stackspace is disposable by design — a tangled one can be deleted and re-imported — but a change you have not sent lives only there, and until now nothing warned you before you threw it away. A project whose tree has moved away from what was imported is flagged as having unsent changes, along with the command that extracts them.

It compares trees rather than counting commits, so a history you amended or rebased without changing content is correctly quiet. It reports what has changed rather than what reached a fork, since `send` leaves no record behind, so a project stays flagged until upstream's own history moves on. And where it cannot tell — a stackspace whose history no longer contains rig's own import commit — it says so rather than reporting that there is nothing to send, because that is the one wrong answer that loses work.
