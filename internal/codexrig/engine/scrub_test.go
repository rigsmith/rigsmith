package engine

import (
	"os"

	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"strings"
	"testing"
)

// A line too big to be a JSON record used to end the scrub as though the file
// had ended: the rest of that line and every line after it were dropped, and
// the staged rollout was a silent truncation of the conversation. Losing the
// backup's content is worse than not redacting it, so the file is copied whole.
func TestAnOversizeLineCopiesTheWholeRolloutRatherThanTruncatingIt(t *testing.T) {
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	var b strings.Builder
	b.WriteString(`{"timestamp":"2026-09-05T11:22:59.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"01a0722a-7356-7592-922a-336289bdc101","cwd":` + jsonPath(m.project()) + `}}` + "\n")
	// One record past the 8 MiB line cap...
	b.WriteString(`{"ordinal":1,"type":"response_item","payload":{"text":"` + strings.Repeat("z", 9<<20) + `"}}` + "\n")
	// ...and a perfectly ordinary one after it, which is what used to vanish.
	const tail = "the line after the enormous one"
	b.WriteString(`{"ordinal":2,"type":"response_item","payload":{"text":"` + tail + `"}}` + "\n")
	want := b.String()
	m.write(t, rolloutRel, want)

	cfg, mc := m.cfg(true)
	staging := t.TempDir()
	if _, err := Sync(Options{
		StagingDir: staging, Config: cfg, Machine: mc, CodexVersion: "0.144.6",
		MaxFileBytes: config.DefaultMaxFileBytes, LargeFileBytes: config.DefaultLargeFileBytes,
		RedactRollouts: true,
		SourceOverride: map[string]string{config.RootCLI: m.codex},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(stagedPath(staging, rolloutRel))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), tail) {
		t.Error("the line after the oversize one was dropped — the rollout was truncated")
	}
	if len(got) != len(want) {
		t.Errorf("staged %d bytes, source is %d", len(got), len(want))
	}
}
