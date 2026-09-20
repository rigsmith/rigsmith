package testgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/rigsmith/rigsmith"

// The packages whose job is running git. A test binary that links one of them
// can put a repo on disk, so it needs this package's import to keep the machine
// out of that repo.
//
// A package that reaches for exec.Command("git", ...) itself is invisible here —
// there is no honest way to tell that apart from the several places that merely
// name git as a tool to look for. Those import this package by hand; this gate
// covers the route almost everything takes.
var gitHelpers = []string{
	module + "/core/gitrepo",
	module + "/core/gitutil",
	module + "/internal/agentrig/backupgit",
}

// Nothing else would notice a new test package missing the import. It would
// pass on CI, which has no ambient git setup to leak, and fail on a developer's
// machine — intermittently, in whichever test happened to measure .git or
// happened to have its TempDir swept while a background writer was still in it.
// So the gate is here: the build that adds such a package fails until the import
// is there too.
func TestEveryTestBinaryReachingGitIsHermetic(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	root := repoRoot(t)
	cmd := exec.Command("go", "list", "-test", "-f", "{{.ImportPath}}|{{join .Deps \" \"}}", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	self := module + "/internal/testgit"
	var missing []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, deps, ok := strings.Cut(line, "|")
		if !ok || !strings.HasSuffix(name, ".test") {
			continue
		}
		pkg := strings.TrimSuffix(name, ".test")
		if pkg == self || !reaches(deps, gitHelpers) {
			continue
		}
		if !reaches(deps, []string{self}) {
			missing = append(missing, pkg)
		}
	}
	for _, pkg := range missing {
		t.Errorf("%s runs git in its tests but is not hermetic — add a %s holding:\n\timport _ %q",
			pkg, filepath.Join(strings.TrimPrefix(pkg, module+"/"), "hermeticgit_test.go"), self)
	}
}

func reaches(deps string, want []string) bool {
	for _, d := range strings.Fields(deps) {
		for _, w := range want {
			if d == w {
				return true
			}
		}
	}
	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working directory")
		}
		dir = parent
	}
}
