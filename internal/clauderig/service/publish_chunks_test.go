package service_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

func putChunked(t *testing.T, root, path, body string) {
	t.Helper()
	name := filepath.Join(root, path)
	modified := time.Unix(0, 0)
	if info, err := os.Stat(name); err == nil {
		// A rewritten index can have the same length. Advance its mtime so
		// Git cannot reuse cached bytes under coarse Windows stat matching.
		modified = info.ModTime().Add(time.Second)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := transcript.Write(name, strings.NewReader(body), modified); err != nil {
		t.Fatal(err)
	}
	// Match normal retention: obsolete partial chunks disappear from both tips.
	if err := transcript.Clean(filepath.Join(root, "cli/projects")); err != nil {
		t.Fatal(err)
	}
}
func readPublishedTranscript(t *testing.T, remote, path string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "restored")
	git(t, filepath.Dir(clone), "clone", "--branch", "main", remote, clone)
	raw, err := os.ReadFile(filepath.Join(clone, path))
	if err != nil || !transcript.IsIndex(raw) {
		t.Fatalf("lost chunked representation: %v", err)
	}
	data, err := transcript.ReadFile(filepath.Join(clone, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPublishArtifactRecoversChunkConflicts(t *testing.T) {
	for _, kind := range []string{"append", "uuid", "edited", "missing", "corrupt", "secret"} {
		t.Run(kind, func(t *testing.T) {
			input, remote := publicationFixture(t, true, false)
			stage := input.Commit.Capture.Sync.StagingDir
			if kind == "edited" {
				// Exercise coarse Windows-style stat matching on every platform.
				git(t, stage, "config", "core.trustctime", "false")
				git(t, stage, "config", "core.checkStat", "minimal")
			}
			const path = "cli/projects/-p/chunked.jsonl"
			base := "{\"uuid\":\"base\"}\n"
			local := "{\"uuid\":\"local\"}\n"
			incomingTail := "{\"uuid\":\"remote\"}\n"
			putChunked(t, stage, path, base)
			git(t, stage, "add", ".")
			git(t, stage, "commit", "-m", "shared chunked base")
			git(t, stage, "push", remote.dir, "HEAD:main")
			incoming := filepath.Join(t.TempDir(), "incoming")
			git(t, filepath.Dir(incoming), "clone", "--branch", "main", remote.dir, incoming)
			theirBody := base + incomingTail
			switch kind {
			case "uuid":
				theirBody = base + "{\"uuid\":\"local\",\"different\":true}\n"
			case "edited":
				theirBody = "{\"uuid\":\"edited\"}\n" + incomingTail
			case "secret":
				theirBody = base + "{\"uuid\":\"remote\",\"text\":\"ghp_" + strings.Repeat("z", 40) + "\"}\n"
			}
			putChunked(t, stage, path, base+local)
			putChunked(t, incoming, path, theirBody)
			if kind == "missing" || kind == "corrupt" {
				entries, err := os.ReadDir(filepath.Join(incoming, path+transcript.Suffix))
				if err != nil {
					t.Fatal(err)
				}
				part := filepath.Join(incoming, path+transcript.Suffix, entries[0].Name())
				if kind == "missing" {
					err = os.Remove(part)
				} else {
					err = os.WriteFile(part, []byte("corrupt"), 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			git(t, stage, "add", ".")
			git(t, stage, "commit", "-m", "local append")
			git(t, incoming, "add", ".")
			git(t, incoming, "commit", "-m", "remote append")
			git(t, incoming, "push", "origin", "HEAD:main")
			if kind == "edited" {
				for _, dir := range []string{stage, incoming} {
					if git(t, dir, "rev-parse", "HEAD:"+path) != git(t, dir, "hash-object", "--no-filters", path) {
						t.Fatal("fixture did not commit the rewritten chunk index")
					}
				}
			}
			before := git(t, remote.dir, "rev-parse", "main")
			localHead := git(t, stage, "rev-parse", "HEAD")
			read := func(p string) []byte {
				b, err := os.ReadFile(filepath.Join(stage, p))
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			index, config, owner := read(".git/index"), read(".git/config"), read(path)
			result, err := (service.Service{}).PublishArtifact(t.Context(), input)
			if localHead != git(t, stage, "rev-parse", "HEAD") || !bytes.Equal(index, read(".git/index")) || !bytes.Equal(config, read(".git/config")) || !bytes.Equal(owner, read(path)) {
				t.Fatal("changed canonical staging")
			}
			if kind != "append" {
				if err == nil || result != (commitartifact.Publication{}) || remote.pushes != 0 || before != git(t, remote.dir, "rev-parse", "main") {
					t.Fatalf("unsafe publication: %+v %v", result, err)
				}
				if kind == "secret" && !errors.Is(err, engine.ErrSecretTripwire) {
					t.Fatal("secret bypassed whole-tree audit", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := readPublishedTranscript(t, remote.dir, path); got != base+local+incomingTail {
				t.Fatalf("lost chunked appends: %q", got)
			}
			if _, err := (service.Service{}).PublishArtifact(t.Context(), input); err != nil || remote.pushes != 1 {
				t.Fatal("replay pushed again", err, remote.pushes)
			}
		})
	}
}
