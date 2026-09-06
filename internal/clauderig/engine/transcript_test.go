package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
)

// A key pasted into a chat arrives the way this one does: one JSON record, the
// block in a string value with its newlines escaped. The text rule spans it, so
// it must be scrubbed rather than refused — refusing blocks the sync for ever,
// because nothing about the transcript will ever change again.
func TestRedactTranscript_ScrubsKeyBlockInJSONRecord(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.jsonl")
	key := "-----BEGIN RSA PRIVATE KEY-----\\nMIIEowIBAAKCAQEA1234\\n-----END RSA PRIVATE KEY-----"
	write(t, dir, "s.jsonl", `{"type":"user","text":"here it is: `+key+` — mind it"}`+"\n")

	dst := filepath.Join(dir, "out.jsonl")
	hits, err := redactTranscript(dst, src, time.Now())
	if err != nil {
		t.Fatalf("redactTranscript: %v", err)
	}
	got := read(t, dst)
	if strings.Contains(got, "PRIVATE KEY") || strings.Contains(got, "MIIEowIBAAKCAQEA") {
		t.Errorf("key survived the scrub: %s", got)
	}
	if !strings.Contains(got, redact.Placeholder) {
		t.Errorf("no placeholder written: %s", got)
	}
	if len(hits) == 0 {
		t.Error("scrub reported no hits")
	}
	// The line either side of the key is prose and has to come through intact.
	if !strings.Contains(got, "here it is:") || !strings.Contains(got, "mind it") {
		t.Errorf("surrounding prose damaged: %s", got)
	}
	// And what the scrubber wrote must satisfy the scanner that guards the push.
	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if finding, err := redact.ScanReader("s.jsonl", f); err != nil {
		t.Fatal(err)
	} else if finding != nil {
		t.Errorf("scanner still finds %s after the scrub", finding.Kind)
	}
}

// The case the refusal exists for: a header with its body on the lines below.
// The loop scrubs a line at a time, so those lines would be copied through
// untouched — and the scanner matches only the header, so nothing downstream
// would notice. Still refused.
func TestRedactTranscript_RefusesKeyBlockSpanningLines(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "s.jsonl", "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234\n-----END RSA PRIVATE KEY-----\n")

	_, err := redactTranscript(filepath.Join(dir, "out.jsonl"), filepath.Join(dir, "s.jsonl"), time.Now())
	if !errors.Is(err, errPrivateKeyInTranscript) {
		t.Fatalf("err = %v, want errPrivateKeyInTranscript", err)
	}
}

// A refused sync has still finished its staging pass, so the marker recording
// what that pass scrubbed has to be written. Leaving it stale made every later
// run re-scrub every transcript — thousands of files, minutes of work — and
// then refuse on the same finding, forever.
func TestSync_RecordsRedactionSettingWhenTripwireRefuses(t *testing.T) {
	live := t.TempDir()
	write(t, live, "projects/-Users-john-Git-rigsmith/s.jsonl",
		`{"type":"user","cwd":"/Users/john/Git/rigsmith","isSidechain":false}`+"\n")
	// Trips the wire, and is not something the scrubber rewrites.
	write(t, live, "plugins/data/leak.json", `{"saved":"ghp_aaaaaaaaaaaaaaaaaaaaa"}`)

	staging := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	if _, err := Sync(Options{
		StagingDir: staging, Config: cliOnlyConfig(live), Machine: m,
		RedactTranscripts: true, SourceOverride: override("cli", live),
	}); err == nil {
		t.Fatal("expected the tripwire to refuse")
	}
	if !redactedLastRun(staging) {
		t.Error("refused sync did not record that it scrubbed; the next run will redo the whole pass")
	}
}

// The shape that actually turns up: a key quoted mid-conversation, truncated,
// with no footer. Testing for a closing marker rejected exactly these — every
// PEM line in the transcript that prompted this fix had BEGIN and no END.
func TestRedactTranscript_ScrubsTruncatedKeyWithNoFooter(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "s.jsonl",
		`{"type":"user","text":"it starts -----BEGIN RSA PRIVATE KEY-----\\nMIIEowIBAAKCAQEA1234 and is cut off"}`+"\n")

	dst := filepath.Join(dir, "out.jsonl")
	if _, err := redactTranscript(dst, filepath.Join(dir, "s.jsonl"), time.Now()); err != nil {
		t.Fatalf("redactTranscript: %v", err)
	}
	got := read(t, dst)
	if strings.Contains(got, "PRIVATE KEY") || strings.Contains(got, "MIIEowIBAAKCAQEA") {
		t.Errorf("truncated key survived: %s", got)
	}
	if !strings.Contains(got, "it starts") {
		t.Errorf("prose before the key was lost: %s", got)
	}
}

// The case no setting could clear: a transcript staged before redaction was on,
// whose live source has since gone (worktree deleted). The walk never sees it
// again, so it kept its secrets and the tripwire refused every sync from then
// on, for ever. The sweep scrubs the staged copy itself.
func TestSync_ScrubsStagedTranscriptWhoseSourceIsGone(t *testing.T) {
	live := t.TempDir()
	write(t, live, "projects/-Users-john-Git-rigsmith/live.jsonl",
		`{"type":"user","cwd":"/Users/john/Git/rigsmith","isSidechain":false}`+"\n")

	staging := filepath.Join(t.TempDir(), "repo")
	// Staged by an earlier run, from a project that no longer exists.
	write(t, staging, "cli/projects/-Users-john-Git-gone/orphan.jsonl",
		`{"type":"user","text":"token ghp_aaaaaaaaaaaaaaaaaaaaa here"}`+"\n")

	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{
		StagingDir: staging, Config: cliOnlyConfig(live), Machine: m,
		RedactTranscripts: true, SourceOverride: override("cli", live),
	})
	if err != nil {
		t.Fatalf("sync refused: %v", err)
	}
	if rep.OrphansScrubbed != 1 {
		t.Errorf("OrphansScrubbed = %d, want 1", rep.OrphansScrubbed)
	}
	got := read(t, filepath.Join(staging, "cli", "projects", "-Users-john-Git-gone", "orphan.jsonl"))
	if strings.Contains(got, "ghp_") {
		t.Errorf("orphan still holds its token: %s", got)
	}
	if !strings.Contains(got, redact.Placeholder) {
		t.Errorf("orphan not scrubbed: %s", got)
	}
}
