package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

func TestMergePublicationAudit(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "reconcile"
		if manual {
			name = "command"
		}
		t.Run(name, func(t *testing.T) {
			for _, shape := range []string{"clean", "conflicted", "fast-forward"} {
				t.Run(shape, func(t *testing.T) {
					for _, secret := range []bool{false, true} {
						name := "safe"
						if secret {
							name = "secret"
						}
						t.Run(name, func(t *testing.T) {
							ctx := context.Background()
							rel := "cli/projects/p/memory/MEMORY.md"
							key := "ghp_" + strings.Repeat("z", 40)
							body := "- [remote](remote.md) remote note"
							if secret {
								body += " " + key
							}
							body += "\n"
							ours := "- [base](base.md)\n"
							if shape == "conflicted" {
								ours = "- [local](local.md)\n"
							}
							repo := divergedRepos(t, map[string][3]string{
								"a.md": {"base\n", "local\n", "base\n"},
								rel:    {"- [base](base.md)\n", ours, body},
							})
							if shape == "fast-forward" {
								mustGit(t, repo.Dir, "reset", "--hard", "HEAD^")
							}
							var run func() error
							if manual {
								home := t.TempDir()
								t.Setenv("HOME", home)
								t.Setenv("USERPROFILE", home)
								dir, err := config.Dir()
								if err != nil {
									t.Fatal(err)
								}
								if err := os.MkdirAll(dir, 0o755); err != nil {
									t.Fatal(err)
								}
								cfg := config.Default()
								cfg.Remote = filepath.Join(filepath.Dir(repo.Dir), "remote.git")
								if err := config.Save(cfg, dir); err != nil {
									t.Fatal(err)
								}
								stage, err := config.StagingDir()
								if err != nil {
									t.Fatal(err)
								}
								if err := os.Rename(repo.Dir, stage); err != nil {
									t.Fatal(err)
								}
								repo, err = gitrepo.Open(ctx, stage)
								if err != nil {
									t.Fatal(err)
								}
								run = func() error {
									cmd := NewMergeCmd()
									cmd.SetOut(new(bytes.Buffer))
									cmd.SetErr(new(bytes.Buffer))
									cmd.SetArgs([]string{"--json"})
									return cmd.ExecuteContext(ctx)
								}
							} else {
								run = func() error { return reconcile(ctx, new(bytes.Buffer), repo, "origin", "main", false) }
							}
							before, err := repo.Head(ctx)
							if err != nil {
								t.Fatal(err)
							}
							err = run()
							if secret {
								if err == nil || !strings.Contains(err.Error(), "secret tripwire") {
									t.Fatalf("secret merge not refused: %v", err)
								}
								if strings.Contains(err.Error(), key) {
									t.Fatal("diagnostic leaked credential")
								}
								after, err := repo.Head(ctx)
								if err != nil || after != before || !repo.InMerge(ctx) {
									t.Fatalf("refused merge advanced HEAD or cannot resume: %v", err)
								}
								// An unstaged cleanup must not hide the old index from the audit.
								put(t, repo.Dir, rel, "cleaned note\n")
								if err := run(); err == nil || !strings.Contains(err.Error(), "unstaged") {
									t.Fatalf("committed different staged bytes: %v", err)
								}
								mustGit(t, repo.Dir, "add", "--", rel)
								if err := run(); err != nil {
									t.Fatalf("cleaned merge cannot resume: %v", err)
								}
							} else if err != nil {
								t.Fatal(err)
							}
							after, err := repo.Head(ctx)
							if err != nil || after == before || repo.InMerge(ctx) {
								t.Fatalf("safe merge not committed: %v", err)
							}
							if err := run(); err != nil {
								t.Fatalf("already integrated merge failed: %v", err)
							}
							again, err := repo.Head(ctx)
							if err != nil || again != after {
								t.Fatalf("no-op merge changed HEAD: %v", err)
							}
						})
					}
				})
			}
		})
	}
}
