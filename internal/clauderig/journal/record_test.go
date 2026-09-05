package journal

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
)

// "2 files too large" says a conversation was not backed up without saying
// which one. The count alone is unanswerable an hour later, and this record
// exists precisely so a sync's outcome survives the process that produced it.
func TestFromSync_NamesTheFilesTheCapLeftOut(t *testing.T) {
	rep := &engine.Report{Roots: []engine.RootResult{{
		ID:    "cli",
		Files: 5,
		Oversize: []engine.OversizeFile{
			{Rel: "projects/-p/marathon.jsonl", Bytes: 92 << 20},
			{Rel: "projects/-q/other.jsonl", Bytes: 51 << 20},
		},
	}}}

	rec := FromSync("Pro16", rep, nil)
	if rec.Oversize != 2 {
		t.Fatalf("Oversize = %d, want 2", rec.Oversize)
	}
	if len(rec.OversizeFiles) != 2 {
		t.Fatalf("OversizeFiles = %v, want both named", rec.OversizeFiles)
	}
	if rec.OversizeFiles[0].Path != "cli/projects/-p/marathon.jsonl" {
		t.Errorf("path = %q, want it qualified by its root", rec.OversizeFiles[0].Path)
	}
	if rec.OversizeFiles[0].Bytes != 92<<20 {
		t.Errorf("bytes = %d, want the size that got it dropped", rec.OversizeFiles[0].Bytes)
	}
	// The summary still reads the same; the list is what it was missing.
	if !strings.Contains(rec.Summary(), "2 files too large") {
		t.Errorf("summary = %q", rec.Summary())
	}
}

// A first sync over a tree of marathon transcripts must not write a record
// longer than anything will show.
func TestFromSync_BoundsTheOversizeList(t *testing.T) {
	var many []engine.OversizeFile
	for i := range MaxRedactedFiles + 20 {
		many = append(many, engine.OversizeFile{Rel: fmt.Sprintf("projects/-p/%d.jsonl", i), Bytes: 1 << 30})
	}
	rec := FromSync("Pro16", &engine.Report{Roots: []engine.RootResult{{ID: "cli", Oversize: many}}}, nil)

	if rec.Oversize != len(many) {
		t.Errorf("the count should still be the true total: %d", rec.Oversize)
	}
	if len(rec.OversizeFiles) != MaxRedactedFiles {
		t.Errorf("named %d files, want the list capped at %d", len(rec.OversizeFiles), MaxRedactedFiles)
	}
}
