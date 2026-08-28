package contents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		rel, want string
	}{
		{"cli/projects/-p/abc.jsonl", "transcripts"},
		{"cli/projects/-p/abc.jsonl.pre-import", "transcript backups"},
		{"cli/projects/-p/memory/note.md", "memory"},
		{"cli/projects/-p/MEMORY.md", "memory"},
		{"cli/projects/-p/toolu_01.txt", "attachments & tool output"},
		{"cli/projects/-p/page-1.jpg", "attachments & tool output"},
		{"cli/skills/a/SKILL.md", "skills, commands & agents"},
		{"cli/plans/p.md", "skills, commands & agents"},
		{"cli/plugins/data/x.json", "plugins"},
		{"cli/settings.json", "config"},
		{"desktop/claude-code-sessions/acct/org/local_a.json", "Desktop session index"},
		{"desktop@work/claude-code-sessions/acct/org/local_a.json", "Desktop session index"},
		{"desktop@work/data/claude_desktop_config.json", "Desktop config"},
		{"journal/machine.jsonl", "clauderig records"},
		{"index/machine.jsonl", "clauderig records"},
		{"clauderig-devices.json", "clauderig records"},
	} {
		if got, _ := classify(tc.rel); got != tc.want {
			t.Errorf("classify(%q) = %q, want %q", tc.rel, got, tc.want)
		}
	}
}

// A backup is a .jsonl too, and memory lives under projects/ like a transcript
// does. Ordering is what keeps each of them out of the transcript bucket, so it
// is worth a test of its own rather than trusting the table above to stay put.
func TestClassify_SpecificRulesBeatTheGeneralOnes(t *testing.T) {
	if got, _ := classify("cli/projects/-p/a.jsonl.pre-import"); got == "transcripts" {
		t.Error("a backup was counted as a transcript")
	}
	if got, _ := classify("cli/projects/-p/memory/a.jsonl"); got != "memory" {
		t.Errorf("memory holding a .jsonl was counted as %q", got)
	}
}

func TestScan_BucketsAndTotals(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, n int) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cli/projects/-p/a.jsonl", 3000)
	write("cli/projects/-p/b.jsonl", 1000)
	write("cli/projects/-p/a.jsonl.pre-import", 500)
	write("cli/projects/-p/memory/m.md", 100)
	write("journal/machine.jsonl", 10)
	// History is reported separately and is not part of what is being kept.
	write(".git/objects/ab/cdef", 999999)

	rep, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 5 {
		t.Errorf("Files = %d, want 5 (.git excluded)", rep.Files)
	}
	if rep.Bytes != 4610 {
		t.Errorf("Bytes = %d, want 4610", rep.Bytes)
	}
	if len(rep.Groups) == 0 || rep.Groups[0].Name != "transcripts" {
		t.Fatalf("largest group = %+v, want transcripts first", rep.Groups)
	}
	if rep.Groups[0].Files != 2 || rep.Groups[0].Bytes != 4000 {
		t.Errorf("transcripts = %d files / %d bytes, want 2 / 4000", rep.Groups[0].Files, rep.Groups[0].Bytes)
	}
}

// A tail of rows all reading 0% buries the one line that matters under its own
// precision.
func TestFold_CollapsesTheTailIntoOther(t *testing.T) {
	rep := Report{
		Bytes: 1000,
		Groups: []Group{
			{Name: "transcripts", Files: 10, Bytes: 970},
			{Name: "plugins", Files: 4, Bytes: 12},
			{Name: "memory", Files: 3, Bytes: 10},
			{Name: "config", Files: 2, Bytes: 8},
		},
	}
	got := rep.Fold()
	if len(got.Groups) != 2 {
		t.Fatalf("groups = %d, want 2 (transcripts + other): %+v", len(got.Groups), got.Groups)
	}
	last := got.Groups[len(got.Groups)-1]
	if last.Name != "other" {
		t.Errorf("last group = %q, want other", last.Name)
	}
	if last.Bytes != 30 || last.Files != 9 {
		t.Errorf("other = %d bytes / %d files, want 30 / 9", last.Bytes, last.Files)
	}
	// The names have to survive somewhere, or the row is unreadable.
	if last.Detail == "" || !strings.Contains(last.Detail, "plugins") {
		t.Errorf("other lost what it contains: %q", last.Detail)
	}
	// Totals must not move — folding is presentation, not arithmetic.
	var sum int64
	for _, g := range got.Groups {
		sum += g.Bytes
	}
	if sum != rep.Bytes {
		t.Errorf("folded groups sum to %d, want %d", sum, rep.Bytes)
	}
}

// Replacing one named row with an "other" containing exactly it loses the name
// and gains nothing.
func TestFold_LeavesASingleSmallCategoryNamed(t *testing.T) {
	rep := Report{
		Bytes: 1000,
		Groups: []Group{
			{Name: "transcripts", Bytes: 990},
			{Name: "memory", Bytes: 10},
		},
	}
	got := rep.Fold()
	if len(got.Groups) != 2 || got.Groups[1].Name != "memory" {
		t.Errorf("a lone small category was folded away: %+v", got.Groups)
	}
}

// "other" stays last even when the fold makes it larger than a kept row.
func TestFold_OtherSortsLast(t *testing.T) {
	rep := Report{
		Bytes: 1000,
		Groups: []Group{
			{Name: "transcripts", Bytes: 800},
			{Name: "attachments", Bytes: 40},
			{Name: "a", Bytes: 19}, {Name: "b", Bytes: 19},
			{Name: "c", Bytes: 19}, {Name: "d", Bytes: 19},
			{Name: "e", Bytes: 19}, {Name: "f", Bytes: 19},
		},
	}
	got := rep.Fold()
	last := got.Groups[len(got.Groups)-1]
	if last.Name != "other" {
		t.Fatalf("last = %q, want other", last.Name)
	}
	if last.Bytes <= got.Groups[1].Bytes {
		t.Skip("fixture no longer exercises the ordering")
	}
}
