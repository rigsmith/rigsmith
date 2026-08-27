---
type: docs
"github.com/rigsmith/rigsmith"
---

The stack design notes now record a build overlay that names each dependency once instead of three times. Writing the swaps by hand repeats every package name in the path, the condition and the removal, which is tiresome past a couple of libraries and gives you three chances to typo a name into a silent no-op. Declaring the sources as a list and selecting them with `Exclude` set arithmetic does the same job from one line per dependency, and as a side effect removes the `%(Filename)` trap that makes a hand-written condition match the wrong package — MSBuild splits `Filename` at the last dot, so a condition meant for one package quietly matches its differently-named sibling. The notes also record why the obvious approach, `%()` item batching, cannot work here.
