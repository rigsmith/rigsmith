package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

func TestAuditScansRawIndexBytes(t *testing.T) {
	key := "ghp_" + strings.Repeat("z", 40)
	for _, extra := range []string{
		`,"unknown":"` + key + `"`,
		`,"parts":[{"sha256":"invalid","size":1,"hidden":"` + key + `"}],"parts":[]`,
	} {
		stage := t.TempDir()
		raw := `{"clauderig_chunked_transcript":1,"size":0,"parts":[]` + extra + "}\n"
		write(t, stage, "cli/projects/p/s.jsonl", raw)
		if _, err := transcript.Decode([]byte(raw)); err != nil {
			t.Fatalf("fixture should decode: %v", err)
		}
		if err := CheckPublish(stage); !errors.Is(err, ErrSecretTripwire) {
			t.Fatalf("credential hidden in physical index needs typed scan rejection: %v", err)
		} else if strings.Contains(err.Error(), key) {
			t.Fatal("diagnostic leaked credential")
		}
	}
}

func TestAuditReadsReferencedPartsOnlyThroughOwner(t *testing.T) {
	stage := t.TempDir()
	p := filepath.Join(stage, "cli/projects/p/s.jsonl")
	if err := transcript.Write(p, strings.NewReader(strings.Repeat("safe\n", 900000)), time.Now()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := transcript.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	unreferenced := filepath.Join(p+transcript.Suffix, "unused.part")
	if err := os.WriteFile(unreferenced, []byte("safe tail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opens := map[string]int{}
	findings, err := audit(stage, func(path string) (transcript.File, error) {
		opens[path]++
		return transcript.Open(path)
	})
	if err != nil || len(findings) != 0 {
		t.Fatalf("safe audit: %v, %v", findings, err)
	}
	if opens[p] != 1 || opens[unreferenced] != 1 {
		t.Fatalf("owner and orphan must each be scanned once: %v", opens)
	}
	for _, part := range idx.Parts {
		if opens[filepath.Join(p+transcript.Suffix, part.Hash+".part")] != 0 {
			t.Fatal("referenced bytes scanned again as raw parts")
		}
	}
}

func TestAuditSkipsNestedGitMetadataOnly(t *testing.T) {
	stage := t.TempDir()
	secret := "ghp_" + strings.Repeat("z", 40)
	write(t, stage, "cli/plugins/nested/.git/config", secret)
	write(t, stage, "cli/plugins/nested/readme.md", "ordinary prose\n")
	if err := CheckPublish(stage); err != nil {
		t.Fatalf("unpublished metadata scanned: %v", err)
	}
	write(t, stage, "cli/plugins/nested/readme.md", secret)
	if err := CheckPublish(stage); err == nil {
		t.Fatal("nested working files escaped audit")
	}
}

// Cancellation must interrupt a file's streaming scan, not merely the next
// directory entry after a potentially multi-gigabyte transcript finishes.
func TestAuditContextCancelsDuringFileScan(t *testing.T) {
	for _, name := range []string{"a.jsonl", "a.bin"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			body := strings.Repeat("safe transcript text\n", 10000)
			if strings.HasSuffix(name, ".bin") {
				body = "\x00" + body
			}
			write(t, root, name, body)
			write(t, root, "z.txt", "later file")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var scanned *cancelAuditFile
			_, err := auditContext(ctx, root, func(path string) (transcript.File, error) {
				if scanned != nil {
					t.Fatal("opened another file after cancellation")
				}
				f, err := transcript.Open(path)
				if err != nil {
					return nil, err
				}
				scanned = &cancelAuditFile{File: f, cancel: cancel}
				return scanned, nil
			})
			if !errors.Is(err, context.Canceled) || scanned.reads != 1 || !scanned.closed {
				t.Fatalf("scan did not stop/close: %+v, %v", scanned, err)
			}
		})
	}
}

type cancelAuditFile struct {
	transcript.File
	cancel context.CancelFunc
	reads  int
	closed bool
}

func (f *cancelAuditFile) Read(b []byte) (int, error) {
	f.reads++
	n, err := f.File.Read(b)
	f.cancel()
	return n, err
}
func (f *cancelAuditFile) Close() error { f.closed = true; return f.File.Close() }
