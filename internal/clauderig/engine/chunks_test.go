package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

func TestChunkSyncImmediateTailRestoreAndRetention(t *testing.T) {
	live, stage, target := t.TempDir(), t.TempDir(), t.TempDir()
	rel := "projects/-p/s.jsonl"
	body := `{"type":"user","cwd":"/p","message":{"role":"user","content":"chunk test"}}` + "\n" + strings.Repeat(`{"type":"assistant","text":"filler"}`+"\n", 270000)
	write(t, live, rel, body)
	opts := Options{StagingDir: stage, Config: cliOnlyConfig(live), Machine: config.Machine{OS: pathmap.OSMacOS, Home: "/Users/test"}, SourceOverride: override("cli", live), ChunkTranscripts: true, MaxFileBytes: 8 << 20, LargeFileBytes: 8 << 20}
	if _, err := Sync(opts); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(stage, "cli", rel)
	raw, _ := os.ReadFile(p)
	idx, err := transcript.Decode(raw)
	if err != nil || idx == nil {
		t.Fatalf("not chunked: %v", err)
	}
	tail := `{"type":"assistant","text":"last turn"}` + "\n"
	write(t, live, rel, body+tail)
	rep, err := Sync(opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Roots[0].Deferred != 0 {
		t.Fatal("chunked tail was throttled")
	}
	got, err := transcript.ReadFile(p)
	if err != nil || string(got) != body+tail {
		t.Fatalf("tail missing: %v", err)
	}
	if rep.LedgerTotal != 1 || rep.LedgerError != "" {
		t.Fatalf("ledger failed: %+v", rep)
	}
	if _, err := Restore(RestoreOptions{StagingDir: stage, Config: opts.Config, Machine: opts.Machine, TargetOverride: override("cli", target)}); err != nil {
		t.Fatal(err)
	}
	native, _ := os.ReadFile(filepath.Join(target, rel))
	if !bytes.Equal(native, got) {
		t.Fatal("restore did not write native bytes")
	}
	if _, err := os.Stat(filepath.Join(target, rel) + transcript.Suffix); !os.IsNotExist(err) {
		t.Fatal("restore copied storage chunks into live tree")
	}
	rollback := opts
	rollback.ChunkTranscripts = false
	if _, err := Sync(rollback); err == nil {
		t.Fatal("rollback should refuse a native file over the configured cap")
	}
	raw, _ = os.ReadFile(p)
	if !transcript.IsIndex(raw) {
		t.Fatal("failed rollback changed snapshot")
	}

	old := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	n, _, err := pruneAgedStagedProjects(filepath.Join(stage, "cli/projects"), time.Now().AddDate(0, 0, -30))
	if err != nil || n != 1 {
		t.Fatalf("retention: %d %v", n, err)
	}
	if _, err := os.Stat(p + transcript.Suffix); !os.IsNotExist(err) {
		t.Fatal("retention orphaned chunks")
	}
}

func TestAuditFindsOldAndCrossChunkSecrets(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "chunked"}[chunked], func(t *testing.T) {
			stage := t.TempDir()
			p := filepath.Join(stage, "cli/projects/-remote/s.jsonl")
			key := "sk-ant-api03-" + strings.Repeat("z", 60)
			// Credential prefix crosses the storage boundary and the scanner boundary.
			body := strings.Repeat(" ", transcript.ChunkSize-5) + key + "\n"
			if chunked {
				if err := transcript.Write(p, strings.NewReader(body), time.Now()); err != nil {
					t.Fatal(err)
				}
			} else {
				write(t, stage, "cli/projects/-remote/s.jsonl", body)
			}
			err := CheckPublish(stage)
			if err == nil {
				t.Fatal("remote-only secret passed publication audit")
			}
			if strings.Contains(err.Error(), key) {
				t.Fatal("error leaked credential")
			}
		})
	}
}

func TestAuditRejectsCorruptChunkAndScansUnreferencedObjects(t *testing.T) {
	stage := t.TempDir()
	p := filepath.Join(stage, "cli/projects/-p/s.jsonl")
	if err := transcript.Write(p, strings.NewReader("safe\n"), time.Now()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	idx, _ := transcript.Decode(raw)
	write(t, stage, "cli/projects/-p/s.jsonl.chunks/unused.part", "ghp_"+strings.Repeat("a", 40))
	if err := CheckPublish(stage); err == nil {
		t.Fatal("unreferenced secret was eligible for git add")
	}
	if err := transcript.Clean(filepath.Join(stage, "cli/projects")); err != nil {
		t.Fatal(err)
	}
	if err := CheckPublish(stage); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(p+transcript.Suffix, idx.Parts[0].Hash+".part")); err != nil {
		t.Fatal(err)
	}
	if err := CheckPublish(stage); err == nil {
		t.Fatal("missing chunk accepted")
	}
}

func TestScrubbingAndChunkingTogether(t *testing.T) {
	live, stage := t.TempDir(), t.TempDir()
	key := "ghp_" + strings.Repeat("z", 36)
	body := strings.Repeat(`{"type":"assistant","text":"ordinary filler"}`+"\n", 210000) + `{"type":"user","text":"` + key + `"}` + "\n"
	write(t, live, "projects/-p/s.jsonl", body)
	opts := Options{StagingDir: stage, Config: cliOnlyConfig(live), Machine: config.Machine{OS: pathmap.OSMacOS, Home: "/Users/test"}, SourceOverride: override("cli", live), ChunkTranscripts: true, RedactTranscripts: true}
	if _, err := Sync(opts); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(stage, "cli/projects/-p/s.jsonl")
	raw, _ := os.ReadFile(p)
	if !transcript.IsIndex(raw) {
		t.Fatal("scrubbed large transcript was not chunked")
	}
	got, err := transcript.ReadFile(p)
	if err != nil || strings.Contains(string(got), key) {
		t.Fatalf("staged secret survived: %v", err)
	}
	rep, err := Sync(opts)
	if err != nil || rep.Roots[0].Files != 0 {
		t.Fatalf("unchanged scrubbed snapshot did not settle: %v", err)
	}
	if original := read(t, filepath.Join(live, "projects/-p/s.jsonl")); original != body {
		t.Fatal("live transcript changed")
	}
}
