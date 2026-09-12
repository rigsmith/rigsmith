package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/agentrig/backupgit"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/peek"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

func TestE2E_GitBytePreservation(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") != "1" {
		t.Skip("gated: CLAUDERIG_E2E=1")
	}
	// Hostile global defaults must be overridden by the committed backup rules,
	// including on the first clone. No user/global Git configuration is edited.
	attrs := filepath.Join(t.TempDir(), "attributes")
	must(t, os.WriteFile(attrs, []byte("* text eol=crlf ident filter=destroy working-tree-encoding=UTF-16\n"), 0o644))
	t.Setenv("GIT_CONFIG_COUNT", "3")
	t.Setenv("GIT_CONFIG_KEY_0", "core.autocrlf")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	t.Setenv("GIT_CONFIG_KEY_1", "core.attributesFile")
	t.Setenv("GIT_CONFIG_VALUE_1", attrs)
	t.Setenv("GIT_CONFIG_KEY_2", "filter.destroy.required")
	t.Setenv("GIT_CONFIG_VALUE_2", "true")
	ctx := t.Context()
	for _, ending := range []string{"\n", "\r\n"} {
		name := "LF"
		if ending == "\r\n" {
			name = "CRLF"
		}
		t.Run(name, func(t *testing.T) {
			live, stage := t.TempDir(), t.TempDir()
			const rel = "projects/p/s.jsonl"
			const native = "projects/p/native.jsonl"
			body := strings.Repeat(`{"type":"user","message":{"content":"ordinary $Id$ text"}}`+ending, 160000)
			raw := "native $Id$ bytes" + ending + "no final newline"
			write(t, live, rel, body)
			write(t, live, native, raw)
			opts := engine.Options{StagingDir: stage, Config: cliOnly(live), Machine: config.Detect("fixture"), ChunkTranscripts: true}
			if _, err := engine.Sync(opts); err != nil {
				t.Fatal(err)
			}
			repo, err := gitrepo.Init(ctx, stage)
			must(t, err)
			must(t, backupgit.Prepare(ctx, stage))
			if changed, err := repo.Commit(ctx, "initial"); err != nil || !changed {
				t.Fatalf("initial commit: %t %v", changed, err)
			}
			write(t, live, rel, body+"tail without newline")
			if _, err := engine.Sync(opts); err != nil {
				t.Fatal(err)
			}
			must(t, backupgit.Prepare(ctx, stage))
			if changed, err := repo.Commit(ctx, "append"); err != nil || !changed {
				t.Fatalf("append commit: %t %v", changed, err)
			}
			bare := filepath.Join(t.TempDir(), "remote.git")
			mustGit(t, filepath.Dir(bare), "init", "--bare", "-b", "main", filepath.Base(bare))
			must(t, repo.SetRemote(ctx, "origin", bare))
			must(t, repo.Push(ctx, "origin", "main"))
			cloned := filepath.Join(t.TempDir(), "repo")
			clone, err := gitrepo.Clone(ctx, bare, cloned)
			must(t, err)
			must(t, engine.CheckPublish(cloned))
			for _, tc := range []struct{ ref, want string }{{"HEAD^", body}, {"HEAD", body + "tail without newline"}} {
				got, err := peek.Read(ctx, clone, tc.ref, peek.Session{Path: "cli/" + rel})
				if err != nil || string(got) != tc.want {
					t.Fatalf("history %s changed bytes: %v", tc.ref, err)
				}
			}
			got, err := transcript.ReadFile(filepath.Join(cloned, "cli", rel))
			if err != nil || !bytes.Equal(got, []byte(body+"tail without newline")) {
				t.Fatalf("clone changed bytes: %v", err)
			}
			target := t.TempDir()
			if _, err := engine.Restore(engine.RestoreOptions{StagingDir: cloned, Config: cliOnly(target), Machine: opts.Machine, TargetOverride: map[string]string{"cli": target}, OverriddenOnly: true}); err != nil {
				t.Fatal(err)
			}
			if read(t, filepath.Join(target, rel)) != body+"tail without newline" || read(t, filepath.Join(target, native)) != raw {
				t.Fatal("restore changed native bytes")
			}
			if read(t, filepath.Join(live, rel)) != body+"tail without newline" {
				t.Fatal("sync changed live source")
			}
		})
	}
}
