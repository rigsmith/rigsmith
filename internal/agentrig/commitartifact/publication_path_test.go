package commitartifact

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func nonportablePublicationComponents() []string {
	names := []string{"NUL", "nul.txt", "CON", "CoN.json", "PRN", "AUX", "AUX .json", "CONIN$", "conout$.txt",
		"COM1", "com9.log", "LPT1", "lpt9.json", "COM¹", "LPT².txt", "COM³", "bad<name", "bad>name", `bad"name`,
		"bad|name", "bad?name", "bad*name", "bad:name", `bad\name`, "trailing.", "trailing ", ".git", ".GiT", "bad\xffname", strings.Repeat("a", 256)}
	for ch := byte(0); ch < 32; ch++ {
		names = append(names, "control"+string(ch)+"name")
	}
	return names
}

func TestPublicationPathUsesPortableComponentsOnEveryHost(t *testing.T) {
	for _, name := range nonportablePublicationComponents() {
		for _, path := range []string{name, "parent/" + name, name + "/child"} {
			if publicationPath(path) {
				t.Errorf("accepted nonportable path %q", path)
			}
		}
	}
	for _, path := range []string{"", "/absolute", "//server/share", ".", "..", "a/../b", "a/./b", "a//b", "a/", strings.Repeat("a/", 2048) + "b"} {
		if publicationPath(path) {
			t.Errorf("accepted unsafe path %q", path)
		}
	}
	for _, path := range []string{"NUL-safe.txt", "COM0", "COM10", "LPT0", "LPT10", "auxiliary", ".gitattributes", "cli/projects/你好/session.jsonl", "Résumé.txt", "config/my settings.json", strings.Repeat("a", 255)} {
		if !publicationPath(path) {
			t.Errorf("refused portable path %q", path)
		}
	}
}

// Write paths directly into Git trees; never create reserved files on the host.
// The same fixtures therefore exercise pre-materialization rejection on all CI
// platforms, including invalid UTF-8 names which Git itself can represent.
func TestPublicationRejectsNonportableGitPathsBeforeAuditOnEveryHost(t *testing.T) {
	repo, parent := seedRepository(t, "sha1")
	blob := mustRun(t, repo, "fixture", "hash-object", "-w", "--stdin")
	for i, name := range []string{"NUL", "COM1.txt", "AUX .json", "conout$", "LPT².log", "a?b", "a*b", "a\x01b", "a\xffb"} {
		t.Run(fmt.Sprintf("fixture-%d", i), func(t *testing.T) {
			subtree := mustRun(t, repo, "100644 blob "+blob+"\t"+name+"\x00", "mktree", "-z")
			tree := mustRun(t, repo, "040000 tree "+subtree+"\tparent\x00", "mktree", "-z")
			sha := mustRun(t, repo, "nonportable fixture\n", "commit-tree", tree, "-p", parent)
			err := repo.checkTree(t.Context(), sha, t.TempDir(), 0, func(context.Context, string) error { t.Fatal("audit saw a nonportable tree"); return nil })
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("nonportable tree returned %v, want ErrInvalid", err)
			}
		})
	}
	// A normal Unicode path can be materialized and audited on every host.
	tree := mustRun(t, repo, "100644 blob "+blob+"\tRésumé.txt\x00", "mktree", "-z")
	sha := mustRun(t, repo, "portable fixture\n", "commit-tree", tree, "-p", parent)
	if err := repo.checkTree(t.Context(), sha, t.TempDir(), 0, func(_ context.Context, root string) error {
		if !filepath.IsAbs(root) {
			t.Fatal("expected private absolute tree")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
