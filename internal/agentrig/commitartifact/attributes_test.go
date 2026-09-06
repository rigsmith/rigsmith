package commitartifact

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckUnsetAttributesUsesTreeRulesAndIgnoresHost(t *testing.T) {
	root := t.TempDir()
	putPublicationFile(t, root, ".gitattributes", "[attr]backup -text -eol -filter -ident -working-tree-encoding\n* backup\n")
	putPublicationFile(t, root, "nested/ignored.txt", "raw\r\n$Id$\n")
	putPublicationFile(t, root, ".gitignore", "nested/\n")
	names := []string{"text", "eol", "filter", "ident", "working-tree-encoding"}
	host := filepath.Join(t.TempDir(), "attrs")
	if err := os.WriteFile(host, []byte("* text filter=host\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.attributesFile")
	t.Setenv("GIT_CONFIG_VALUE_0", host)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "wrong"))
	if err := CheckUnsetAttributes(t.Context(), root, names); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
		t.Fatal("created checkout", err)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			putPublicationFile(t, root, "nested/.gitattributes", "*.txt "+name+"\n")
			if err := CheckUnsetAttributes(t.Context(), root, names); !errors.Is(err, ErrAttributes) {
				t.Fatalf("accepted override: %v", err)
			}
		})
	}
	if err := os.Remove(filepath.Join(root, "nested/.gitattributes")); err != nil {
		t.Fatal(err)
	}
	putPublicationFile(t, root, ".gitattributes", "* text\n")
	if err := CheckUnsetAttributes(t.Context(), root, names); !errors.Is(err, ErrAttributes) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := CheckUnsetAttributes(ctx, root, names); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCheckUnsetAttributesStreamsLargeResponses(t *testing.T) {
	root := t.TempDir()
	putPublicationFile(t, root, ".gitattributes", "* -text -eol -filter -ident -working-tree-encoding\n")
	for i := 0; i < 1100; i++ {
		putPublicationFile(t, root, strings.Repeat("a", 200)+fmt.Sprintf("%04d", i), "bytes")
	}
	if err := CheckUnsetAttributes(t.Context(), root, []string{"text", "eol", "filter", "ident", "working-tree-encoding"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckUnsetAttributes(t.Context(), root, []string{"--all"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
