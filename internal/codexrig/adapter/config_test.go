package adapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/files"
	"github.com/rigsmith/rigsmith/internal/codexrig/configcodec"
)

func putConfig(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureConfigSelectionAndPrivateBytes(t *testing.T) {
	root := t.TempDir()
	fixtures := map[string]string{
		"config.toml":      "model = 'base'\n[env]\nKEY = 'private'",
		"work.config.toml": "model = 'profile'\npassword = 'private'",
		"AGENTS.md":        "private", "hooks.json": "private", "auth.json": "\xffprivate",
		".hidden.config.toml": "private", "bad.name.config.toml": "private",
		"sessions/2026/config.toml": "private", "skills/demo/config.toml": "private",
	}
	for name, body := range fixtures {
		putConfig(t, root, name, body)
	}
	result, err := CaptureConfig(t.Context(), Root{CodexHome, root})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 || result.Files[0].Path != "config.toml" || result.Files[1].Path != "work.config.toml" {
		t.Fatalf("wrong selection: %#v", result)
	}
	for _, f := range result.Files {
		if bytes.Contains(f.Data, []byte("private")) || bytes.Contains(f.Data, []byte(root)) {
			t.Fatalf("private bytes returned in %s", f.Path)
		}
		again, err := configcodec.Capture(f.Data)
		if err != nil || !bytes.Equal(again, f.Data) {
			t.Fatalf("not sanitized: %v", err)
		}
	}
	for name, body := range fixtures {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(data) != body {
			t.Fatalf("source changed %q: %v", name, err)
		}
	}
	if _, err := CaptureConfig(t.Context(), Root{UserSkills, root}); err == nil {
		t.Fatal("accepted wrong root")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := CaptureConfig(t.Context(), Root{CodexHome, missing}); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created missing root")
	}
}

func TestCaptureConfigAllOrError(t *testing.T) {
	for _, bad := range []string{"broken = [", "unknown = '-----BEGIN PRIVATE KEY-----'"} {
		root := t.TempDir()
		putConfig(t, root, "config.toml", "model = 'safe'")
		putConfig(t, root, "z.config.toml", bad)
		result, err := CaptureConfig(t.Context(), Root{CodexHome, root})
		if err == nil || result.Files != nil {
			t.Fatalf("returned partial capture: %#v %v", result, err)
		}
		if bytes.Contains([]byte(err.Error()), []byte(bad)) {
			t.Fatal("source leaked in error")
		}
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "config.toml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if result, err := CaptureConfig(t.Context(), Root{CodexHome, root}); !errors.Is(err, files.ErrSource) || result.Files != nil {
		t.Fatalf("directory silently skipped: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "config.toml")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.toml")
	putConfig(t, filepath.Dir(outside), filepath.Base(outside), "model = 'private'")
	if err := os.Symlink(outside, filepath.Join(root, "config.toml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if result, err := CaptureConfig(t.Context(), Root{CodexHome, root}); !errors.Is(err, files.ErrSource) || result.Files != nil {
		t.Fatalf("link silently skipped: %v", err)
	}
}

func TestCaptureConfigBudgets(t *testing.T) {
	t.Run("file count", func(t *testing.T) {
		root := t.TempDir()
		for i := 0; i <= MaxConfigFiles; i++ {
			putConfig(t, root, fmt.Sprintf("p%d.config.toml", i), "")
		}
		if result, err := CaptureConfig(t.Context(), Root{CodexHome, root}); !errors.Is(err, files.ErrSourceLimit) || result.Files != nil {
			t.Fatalf("count limit: %v", err)
		}
	})
	t.Run("aggregate raw bytes", func(t *testing.T) {
		root := t.TempDir()
		padding := bytes.Repeat([]byte("# padding\n"), 100000)
		for i := 0; i < 9; i++ {
			putConfig(t, root, fmt.Sprintf("p%d.config.toml", i), string(padding))
		}
		if result, err := CaptureConfig(t.Context(), Root{CodexHome, root}); !errors.Is(err, files.ErrSourceLimit) || result.Files != nil {
			t.Fatalf("aggregate limit: %v", err)
		}
	})
	t.Run("per file bytes", func(t *testing.T) {
		root := t.TempDir()
		putConfig(t, root, "config.toml", string(bytes.Repeat([]byte(" "), configcodec.MaxBytes+1)))
		if result, err := CaptureConfig(t.Context(), Root{CodexHome, root}); !errors.Is(err, files.ErrSourceLimit) || result.Files != nil {
			t.Fatalf("file limit: %v", err)
		}
	})
}

type changingSource struct {
	*files.Source
	reads            int
	afterRead        func(int)
	beforeFinalNames func()
	names            int
}

func (s *changingSource) Read(ctx context.Context, name string, limit int64) ([]byte, error) {
	data, err := s.Source.Read(ctx, name, limit)
	s.reads++
	if s.afterRead != nil {
		s.afterRead(s.reads)
	}
	return data, err
}
func (s *changingSource) Names(ctx context.Context, limit int) ([]string, error) {
	s.names++
	if s.names == 2 && s.beforeFinalNames != nil {
		s.beforeFinalNames()
	}
	return s.Source.Names(ctx, limit)
}

func TestCaptureConfigRevalidatesContentsAndNames(t *testing.T) {
	for _, change := range []string{"same-metadata contents", "new profile", "removed profile", "cancel"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			putConfig(t, root, "config.toml", "model = 'first'")
			source, err := files.OpenSource(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			wrapped := &changingSource{Source: source}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wrapped.afterRead = func(n int) {
				if n != 1 {
					return
				}
				switch change {
				case "same-metadata contents":
					path := filepath.Join(root, "config.toml")
					info, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					putConfig(t, root, "config.toml", "model = 'other'")
					if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
						t.Fatal(err)
					}
				case "new profile":
					putConfig(t, root, "new.config.toml", "model = 'new'")
				case "removed profile":
					if err := os.Remove(filepath.Join(root, "config.toml")); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					cancel()
				}
			}
			result, err := captureConfig(ctx, wrapped)
			if err == nil || result.Files != nil {
				t.Fatalf("accepted changed source: %v", err)
			}
		})
	}
}

func TestCaptureConfigIgnoresUnrelatedChurn(t *testing.T) {
	root := t.TempDir()
	putConfig(t, root, "config.toml", "model = 'safe'")
	source, err := files.OpenSource(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	wrapped := &changingSource{Source: source, beforeFinalNames: func() { putConfig(t, root, "history.jsonl", "private") }}
	result, err := captureConfig(t.Context(), wrapped)
	if err != nil || len(result.Files) != 1 {
		t.Fatalf("unrelated churn failed capture: %v", err)
	}
	empty, err := CaptureConfig(t.Context(), Root{CodexHome, t.TempDir()})
	if err != nil || !reflect.DeepEqual(empty.Files, []ConfigFile{}) {
		t.Fatalf("empty capture: %#v %v", empty, err)
	}
}

func TestConfigFilenamePortability(t *testing.T) {
	for _, names := range [][]string{{"work.config.toml", "Work.config.toml"}, {"CON.config.toml"}, {"lpt1.config.toml"}, {"com9.config.toml"}} {
		if _, err := configNames(names); !errors.Is(err, ErrConfigNames) {
			t.Fatalf("accepted platform collision: %v", err)
		}
	}
	names, err := configNames([]string{"config.toml", "com10.config.toml", "auxiliary.config.toml"})
	if err != nil || len(names) != 3 {
		t.Fatalf("ordinary names refused: %v", err)
	}
}

func TestCaptureRefusesCredentialShapedProfileName(t *testing.T) {
	root := t.TempDir()
	name := "sk-" + strings.Repeat("A1", 16) + ".config.toml"
	putConfig(t, root, name, "model = 'safe'")
	result, err := CaptureConfig(t.Context(), Root{CodexHome, root})
	if !errors.Is(err, configcodec.ErrSecret) || result.Files != nil {
		t.Fatalf("credential-shaped filename returned: %v", err)
	}
	if strings.Contains(err.Error(), name) {
		t.Fatal("filename leaked in error")
	}
}

func TestCaptureUsesOneSourceChangeSentinel(t *testing.T) {
	if !errors.Is(ErrConfigSource, files.ErrSourceChanged) || !errors.Is(files.ErrSourceChanged, ErrConfigSource) {
		t.Fatal("source-change identities differ")
	}
	root := t.TempDir()
	putConfig(t, root, "config.toml", "model = 'first'")
	source, err := files.OpenSource(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	wrapped := &changingSource{Source: source, afterRead: func(n int) {
		if n == 1 {
			putConfig(t, root, "config.toml", "model = 'other'")
		}
	}}
	if result, err := captureConfig(t.Context(), wrapped); !errors.Is(err, files.ErrSourceChanged) || result.Files != nil {
		t.Fatalf("content change has wrong identity: %v", err)
	}
}
