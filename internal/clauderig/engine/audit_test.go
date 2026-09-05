package engine

import (
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
		if err := CheckPublish(stage); err == nil {
			t.Fatal("credential hidden in physical index passed audit")
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
