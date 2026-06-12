// Port of the .NET rig's ConventionTests solution-candidate case.
package detect

import (
	"path/filepath"
	"testing"
)

func TestSolutionCandidates_ListSlnxBeforeSln(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "A.sln"), "x")
	writeFile(t, filepath.Join(root, "B.slnx"), "<Solution/>")

	candidates := SolutionCandidates(root)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %v, want 2 entries", candidates)
	}
	if candidates[0] != "B.slnx" { // *.slnx preferred (matches FindSolution)
		t.Fatalf("candidates[0] = %q, want B.slnx", candidates[0])
	}
}
