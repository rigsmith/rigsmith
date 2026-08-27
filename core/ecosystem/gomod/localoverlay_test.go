package gomod

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func writeMod(t *testing.T, dir, module, goVer string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "module " + module + "\n\ngo " + goVer + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGoLocalOverlay(t *testing.T) {
	ctx := context.Background()
	a := New()
	redirects := []plugin.Redirect{{Package: "example.com/acme/lib", Path: "lib/go.mod"}}

	newRoot := func(t *testing.T, appGo, libGo string) string {
		t.Helper()
		root := t.TempDir()
		writeMod(t, filepath.Join(root, "app"), "example.com/you/app", appGo)
		writeMod(t, filepath.Join(root, "lib"), "example.com/acme/lib", libGo)
		return root
	}

	t.Run("lists every module, and says which Go they need", func(t *testing.T) {
		// Omitting the directive is not an option: go.work then defaults to 1.18
		// and refuses to load any module asking for more. Compared numerically,
		// so 1.9 does not outrank 1.24.
		got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{
			Root: newRoot(t, "1.9", "1.24"), Redirects: redirects,
		})
		if err != nil {
			t.Fatal(err)
		}
		body := got.Files[workFile]
		for _, want := range []string{"go 1.24", "./app", "./lib"} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %q in:\n%s", want, body)
			}
		}
	})

	t.Run("nothing required across modules is skipped", func(t *testing.T) {
		got, _ := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: newRoot(t, "1.24", "1.24")})
		if !got.Skipped {
			t.Fatalf("%+v, want skipped when no redirect was asked for", got)
		}
	})

	t.Run("one module alone needs no workspace", func(t *testing.T) {
		root := t.TempDir()
		writeMod(t, filepath.Join(root, "only"), "example.com/only", "1.24")
		got, _ := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects})
		if !got.Skipped {
			t.Fatalf("%+v, want skipped for a single module", got)
		}
	})

	t.Run("writing, then rewriting its own file", func(t *testing.T) {
		root := newRoot(t, "1.24", "1.24")
		if _, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects, Write: true}); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(root, workFile))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), workMarker) {
			t.Fatal("no marker, so a rewrite could not tell it from a hand-written one")
		}
		if _, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects, Write: true}); err != nil {
			t.Fatalf("refused to rewrite its own file: %v", err)
		}
	})

	t.Run("a hand-written go.work is reported, not rewritten", func(t *testing.T) {
		// It is authoritative and may say more than rig knows about, so a missing
		// module is a finding rather than a licence to replace the file.
		root := newRoot(t, "1.24", "1.24")
		mine := "go 1.24\n\nuse ./app\n"
		if err := os.WriteFile(filepath.Join(root, workFile), []byte(mine), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Problems) == 0 || !strings.Contains(got.Problems[0].Message, "lib") {
			t.Fatalf("missing module not reported: %+v", got.Problems)
		}
		if _, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects, Write: true}); err == nil {
			t.Fatal("overwrote a hand-written go.work")
		}
		body, _ := os.ReadFile(filepath.Join(root, workFile))
		if string(body) != mine {
			t.Fatal("changed it anyway")
		}
	})
}
