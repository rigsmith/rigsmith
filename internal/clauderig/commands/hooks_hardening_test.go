package commands

import (
	"bytes"
	"context"
	"github.com/rigsmith/rigsmith/internal/clauderig/hooks"
	"github.com/spf13/cobra"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHardeningScopeCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, sp := range scopeSpecs() {
		if !sp.guide {
			continue
		}
		for _, verb := range []string{"install", "uninstall"} {
			t.Run(sp.use+"-"+verb, func(t *testing.T) {
				root := t.TempDir()
				if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
					t.Fatalf("%s: %v", out, err)
				}
				t.Chdir(root)
				outside := t.TempDir()
				if err := os.Symlink(outside, filepath.Join(root, ".claude")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				target := filepath.Join(outside, "settings.json")
				if sp.use == "local" {
					target = filepath.Join(outside, "settings.local.json")
				}
				if err := os.WriteFile(target, []byte(`{}`), 0644); err != nil {
					t.Fatal(err)
				}
				cmd := &cobra.Command{}
				cmd.SetContext(context.Background())
				cmd.SetOut(&bytes.Buffer{})
				var err error
				if verb == "install" {
					err = scopeInstall(cmd, sp)
				} else {
					err = scopeUninstall(cmd, sp)
				}
				if err == nil {
					t.Fatal("command accepted escaping link")
				}
				raw, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if string(raw) != `{}` {
					t.Fatal("outside settings modified")
				}
			})
		}
	}
}
func TestHardeningCandidateOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"Stop":[{"matcher":"x\u001b","hooks":[{"type":"command","command":"echo clauderig\n\u001b"}]}]}}`), 0644); err != nil {
		t.Fatal(err)
	}
	text := hooks.Candidates(path)
	if bytes.ContainsAny([]byte(text), "\n\x1b") {
		t.Fatalf("unsafe diagnostic %q", text)
	}
	if !bytes.Contains([]byte(text), []byte(`echo clauderig\n\x1b`)) {
		t.Fatalf("command missing: %q", text)
	}
}
