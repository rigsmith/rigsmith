package testgit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/rigsmith/rigsmith"

// The packages whose job is running git. A test binary that links one of them
// can put a repo on disk, so it needs this package's import to keep the machine
// out of that repo.
//
// A package that reaches for exec.Command("git", ...) in its own test files is
// found the other way, by reading them — see directCallers. Grep cannot tell
// that apart from the several places that merely name git as a tool to look
// for, but the syntax tree can.
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
	direct := directCallers(t, root)
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
		if pkg == self {
			continue
		}
		if !reaches(deps, gitHelpers) && !direct[pkg] {
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

// directCallers reads every test file in the module and returns the packages
// whose own tests run git — exec.Command("git", …) and its Context form, under
// whatever name the file imports os/exec as.
//
// The dependency check cannot see these: the package reaches git without
// touching any of the helpers. A grep cannot separate them either, because
// "git" appears in these test files as a tool name to look for, as a subcommand
// in a string, and in URLs. The call expression is unambiguous.
func directCallers(t *testing.T, root string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil // not this test's job to report a parse error
		}
		if !runsGit(f) {
			return nil
		}
		rel, rerr := filepath.Rel(root, filepath.Dir(path))
		if rerr != nil {
			return nil
		}
		// A test file at the module root is `module`, not `module + "/."`, which
		// would match no test binary and quietly excuse the package.
		if rel == "." {
			found[module] = true
		} else {
			found[module+"/"+filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	return found
}

// runsGit reports whether the file calls exec.Command or exec.CommandContext
// with "git" as the program.
func runsGit(f *ast.File) bool {
	pkg := "exec"
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil && p == "os/exec" {
			if imp.Name != nil {
				pkg = imp.Name.Name
			}
		}
	}
	git := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != pkg {
			return true
		}
		// Command takes the program first; CommandContext takes it after ctx.
		var arg int
		switch sel.Sel.Name {
		case "Command":
			arg = 0
		case "CommandContext":
			arg = 1
		default:
			return true
		}
		if arg >= len(call.Args) {
			return true
		}
		lit, ok := call.Args[arg].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if v, err := strconv.Unquote(lit.Value); err == nil && (v == "git" || v == "git.exe") {
			git = true
		}
		return true
	})
	return git
}
