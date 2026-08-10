---
type: fix
"github.com/rigsmith/rigsmith"
---

Running `changerig` or `shiprig` outside a configured repo now exits 0 with guidance, instead of exiting 1.

Both route their bare invocation to `add` and `status`, which need a configured workspace, so merely running the binary anywhere else failed — while `rig` and `clauderig` exit 0 from the same place. Anything that probes an installed CLI sees that first: winget's validation VM runs the executable after installing a package, records the non-zero exit against the install, and labels the submission `Validation-Executable-Error`. It did so for ChangeRig in 1.4.0 (16 days to merge) and again in 1.5.1.

Only a genuinely bare run is softened. `changerig -m "…"`, `shiprig status`, and the `add`/`status` subcommands invoked by name all still exit non-zero in an unconfigured repo — a CI gate calls `status` explicitly, and that contract is unchanged and now covered by its own test. The guidance text is identical either way; only the exit code moved.
