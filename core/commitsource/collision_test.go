package commitsource

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/gitutil"
)

// A real repository whose history holds two commits sharing their first 7
// hash characters, read with LogSince and synthesized: the two changesets
// get distinct IDs. The commits are siblings (same parent and tree, different
// messages), found by a birthday search over their object hashes and written
// with hash-object; a merge puts both in HEAD's history.
func TestSynthesizeIDsFromARealPrefixCollision(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	file := filepath.Join(dir, "packages", "pkg-a", "a.txt")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	run("", "init", "-q", "-b", "main")
	if err := os.WriteFile(file, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("", "add", "-A")
	run("", "commit", "-q", "-m", "init")
	parent := run("", "rev-parse", "HEAD")
	if err := os.WriteFile(file, []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("", "add", "-A")
	tree := run("", "write-tree")

	body := func(n int) string {
		return fmt.Sprintf("tree %s\nparent %s\nauthor T <t@example.com> 1700000000 +0000\ncommitter T <t@example.com> 1700000000 +0000\n\nfix: change %d\n", tree, parent, n)
	}
	hash := func(content string) string {
		sum := sha1.Sum([]byte(fmt.Sprintf("commit %d\x00%s", len(content), content)))
		return hex.EncodeToString(sum[:])
	}
	seen := map[string]int{}
	var a, b int
	for n := 0; ; n++ {
		if n > 1<<20 {
			t.Fatal("no 7-character collision in 2^20 tries")
		}
		prefix := hash(body(n))[:7]
		if m, ok := seen[prefix]; ok {
			a, b = m, n
			break
		}
		seen[prefix] = n
	}
	first := run(body(a), "hash-object", "-t", "commit", "-w", "--stdin")
	second := run(body(b), "hash-object", "-t", "commit", "-w", "--stdin")
	if first[:7] != second[:7] || first == second {
		t.Fatalf("fixture: %s and %s don't share 7 characters", first, second)
	}
	merge := run("", "commit-tree", tree, "-p", first, "-p", second, "-m", "Merge the siblings")
	run("", "update-ref", "HEAD", merge)

	commits, err := gitutil.LogSince(context.Background(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	got := Synthesize(commits, pkgs(), dir, config.Default())
	idOf := map[string]string{}
	for _, cs := range got {
		idOf[cs.Commit] = cs.ID
	}
	one, two := idOf[first], idOf[second]
	if one == "" || two == "" {
		t.Fatalf("the colliding commits weren't both synthesized: %v", idOf)
	}
	if one == two {
		t.Errorf("both commits got ID %q", one)
	}
	if !strings.HasPrefix(first, one) || !strings.HasPrefix(second, two) {
		t.Errorf("IDs %q, %q aren't prefixes of %s, %s", one, two, first, second)
	}
}
