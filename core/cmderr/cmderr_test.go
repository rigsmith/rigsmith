package cmderr

import (
	"fmt"
	"strings"
	"testing"
)

// TestDetailKeepsWhicheverStreamSpoke pins the reason this package exists: a
// tool that reports its failure on stdout (dotnet, vpk) must still explain
// itself. Wrapping stderr alone reduced a rejected push to "exit status 1:"
// with nothing after the colon.
func TestDetailKeepsWhicheverStreamSpoke(t *testing.T) {
	const nugetErr = "warn : No API Key was provided\nerror: Response status code does not indicate success: 403 (Forbidden)."

	if got := Detail(nugetErr, ""); got != nugetErr {
		t.Errorf("stdout-only detail = %q, want the tool's diagnostics", got)
	}
	if got := Detail("", "boom"); got != "boom" {
		t.Errorf("stderr-only detail = %q, want %q", got, "boom")
	}
	// Both streams spoke: keep both, stderr first — a tool can put a generic
	// line on one and the reason on the other.
	if got := Detail("from stdout", "from stderr"); got != "from stderr\nfrom stdout" {
		t.Errorf("two-stream detail = %q, want both", got)
	}
	// A silent failure still reads as a sentence, not a dangling colon.
	if got := Detail("  ", " "); got != "(no output)" {
		t.Errorf("silent failure detail = %q, want (no output)", got)
	}
}

// TestDetailBoundsALongLog keeps a build tool's whole log out of the message
// while retaining the tail, where the error summary lives.
func TestDetailBoundsALongLog(t *testing.T) {
	var log strings.Builder
	for i := range 100 {
		fmt.Fprintf(&log, "line %d\n", i)
	}
	log.WriteString("error CS1002: ; expected")

	got := Detail(log.String(), "")
	if !strings.Contains(got, "error CS1002: ; expected") {
		t.Errorf("detail dropped the tail, where the error is: %q", got)
	}
	if strings.Contains(got, "line 0\n") {
		t.Error("detail should not carry the whole build log")
	}
	if n := len(strings.Split(got, "\n")); n > TailLines {
		t.Errorf("detail = %d lines, want at most %d", n, TailLines)
	}
}

func TestLastLines(t *testing.T) {
	if got := LastLines("", 3); got != "" {
		t.Errorf("LastLines(empty) = %q, want empty", got)
	}
	if got := LastLines("only", 3); got != "only" {
		t.Errorf("LastLines(shorter than n) = %q, want the whole string", got)
	}
	if got := LastLines("a\nb\nc\nd", 2); got != "c\nd" {
		t.Errorf("LastLines = %q, want the final 2 lines", got)
	}
}
