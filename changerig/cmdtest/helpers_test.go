// Package cmdtest drives the changerig (and, for `tag`, shiprig) binaries
// end-to-end, porting the command-level suites from net-changesets
// (tests/Changesets.Tests/**/ *CommandTests.cs) onto the Go CLI contract.
//
// The harness mirrors changerig/parity/harness_test.go: TestMain builds each
// binary once from the repo root (located via go.work), every test runs the
// real binary in a hermetic t.TempDir workspace, and git-dependent tests use
// real local repositories (the pattern from core/gitutil/gitutil_test.go).
//
// Assertions target the Go tool's actual messages and exit behavior, not the
// C# text or exit codes: failures only need to be non-zero, and message checks
// are case-insensitive substrings (fang re-capitalizes error text).
package cmdtest

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var (
	changerigBin string
	shiprigBin   string
)

func TestMain(m *testing.M) {
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cmdtest: locate repo root:", err)
		os.Exit(1)
	}

	tmp, err := os.MkdirTemp("", "rigsmith-cmdtest-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "cmdtest: temp dir:", err)
		os.Exit(1)
	}

	changerigBin, err = buildBinary(root, tmp, "changerig", "./changerig")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.RemoveAll(tmp)
		os.Exit(1)
	}
	shiprigBin, err = buildBinary(root, tmp, "shiprig", "./shiprig")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.RemoveAll(tmp)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

func buildBinary(root, tmpDir, name, pkg string) (string, error) {
	bin := filepath.Join(tmpDir, name)
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, pkg)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("cmdtest: build %s: %v\n%s", pkg, err, out)
	}
	return bin, nil
}

func findRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.work not found above %s", filepath.Dir(file))
		}
		dir = parent
	}
}

// --- running the binaries ---

// run executes a binary in dir and returns its exit code plus combined output.
// Any failure other than a non-zero exit (e.g. the binary missing) is fatal.
func run(t *testing.T, bin, dir string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), string(out)
		}
		t.Fatalf("run %s %s: %v\n%s", filepath.Base(bin), strings.Join(args, " "), err, out)
	}
	return 0, string(out)
}

func runChangerig(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	return run(t, changerigBin, dir, args...)
}

func runShiprig(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	return run(t, shiprigBin, dir, args...)
}

// --- assertions ---

// assertContains checks output for a substring, case-insensitively: fang
// re-capitalizes the first letter of error messages, so casing is cosmetic.
func assertContains(t *testing.T, output, want string) {
	t.Helper()
	if !strings.Contains(strings.ToLower(output), strings.ToLower(want)) {
		t.Errorf("output does not contain %q:\n%s", want, output)
	}
}

func assertNotContains(t *testing.T, output, unwanted string) {
	t.Helper()
	if strings.Contains(strings.ToLower(output), strings.ToLower(unwanted)) {
		t.Errorf("output should not contain %q:\n%s", unwanted, output)
	}
}

func assertExitZero(t *testing.T, code int, output string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, output)
	}
}

func assertExitNonZero(t *testing.T, code int, output string) {
	t.Helper()
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\n%s", output)
	}
}

// --- fixtures ---

// tempDir returns a t.TempDir with symlinks resolved (macOS: /var/folders is a
// symlink to /private/var; the binary's os.Getwd sees the physical path, so
// paths written here must match it for the --since path comparisons).
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeNpmWorkspace materializes a minimal npm workspace: a private root
// manifest with workspaces, an empty lockfile (settles detection on npm), and
// one packages/<name>/package.json per entry. Mirrors the parity harness's
// writeNodeRepo / net-changesets' ParityFixtures.WriteNodeRepo.
func writeNpmWorkspace(t *testing.T, root string, versions map[string]string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "package.json"),
		`{ "name": "root", "private": true, "workspaces": ["packages/*"] }`)
	writeFile(t, filepath.Join(root, "package-lock.json"), "{}")
	for name, version := range versions {
		writeFile(t, filepath.Join(root, "packages", name, "package.json"),
			fmt.Sprintf(`{ "name": %q, "version": %q }`, name, version))
	}
}

// initChangesets writes .changeset/config.json (the marker `init` creates).
func initChangesets(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch" }`)
}

// newWorkspace is the common single-package fixture: an initialized npm
// workspace holding pkg-a@1.0.0.
func newWorkspace(t *testing.T) string {
	t.Helper()
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	initChangesets(t, dir)
	return dir
}

// writeChangeset writes a single-package changeset file and returns its path.
func writeChangeset(t *testing.T, root, id, pkg, bump, summary string) string {
	t.Helper()
	path := filepath.Join(root, ".changeset", id+".md")
	writeFile(t, path, fmt.Sprintf("---\n%q: %s\n---\n\n%s\n", pkg, bump, summary))
	return path
}

// changesetFiles lists the changeset markdown files in .changeset (everything
// but README.md and non-.md files like config.json / pre.json).
func changesetFiles(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".changeset"))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || strings.EqualFold(name, "README.md") {
			continue
		}
		files = append(files, filepath.Join(root, ".changeset", name))
	}
	return files
}

// --- git (pattern from core/gitutil/gitutil_test.go) ---

// gitInit turns dir into a git repository on branch main with local identity
// config and commits everything currently in it.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "commit.gpgsign", "false")
	git(t, dir, "config", "tag.gpgsign", "false")
	gitCommitAll(t, dir, "initial")
}

func gitCommitAll(t *testing.T, dir, message string) {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", message)
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// --- file helpers ---

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
