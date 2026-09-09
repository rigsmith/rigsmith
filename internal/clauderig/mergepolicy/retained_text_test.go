package mergepolicy

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
)

func TestRetainedAppendRecovery(t *testing.T) {
	base := "{\"uuid\":\"base\",\"future\":{\"text\":\"unchanged\"}}\r\n"
	common := "{\"uuid\":\"shared\"}\r\n"
	ours := "{\"uuid\":\"ours\",\"parentUuid\":\"shared\"}\n"
	theirs := "{\"uuid\":\"theirs\",\"parentUuid\":\"shared\"}\n"
	unkeyed := "{\"type\":\"summary\"}\n"
	for _, tc := range []struct{ name, path, base, ours, theirs, want string }{
		{"branches", "cli/projects/p/s.jsonl", base, base + common + ours, base + common + theirs, base + common + ours + theirs},
		{"identical uuid", "cli/projects/p/s.jsonl", base, base + ours + common, base + common + theirs, base + ours + common + theirs},
		{"unkeyed records", "cli/projects/p/s.jsonl", base, base + ours + unkeyed, base + theirs + unkeyed, base + ours + unkeyed + theirs + unkeyed},
		{"empty base", "cli/projects/p/s.jsonl", "", ours, theirs, ours + theirs},
		{"memory", "cli/projects/p/memory/MEMORY.md", "# notes\r\n", "# notes\r\ncommon\nlocal\n\n", "# notes\r\ncommon\nremote\n\n", "# notes\r\ncommon\nlocal\n\nremote\n\n"},
		{"longer snapshot", "cli/projects/p/memory/log.txt", "base\n", "base\nnext\nlast\n", "base\nnext\n", "base\nnext\nlast\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b, c := []byte(tc.base), []byte(tc.ours), []byte(tc.theirs)
			out, err := ResolveRetained(t.Context(), tc.path, a, b, c)
			if err != nil || string(out) != tc.want {
				t.Fatalf("merge = %q, %v; want %q", out, err, tc.want)
			}
			again, err := ResolveRetained(t.Context(), tc.path, a, b, c)
			if err != nil || !bytes.Equal(out, again) || string(a) != tc.base || string(b) != tc.ours || string(c) != tc.theirs {
				t.Fatal("nondeterministic result or mutated inputs", err)
			}
		})
	}
}

func TestRetainedAppendRejectsAmbiguousData(t *testing.T) {
	base := "{\"uuid\":\"base\"}\n"
	good := base + "{\"uuid\":\"ours\"}\n"
	for _, bad := range []string{
		"", "{\"uuid\":\"changed-base\"}\n", base + "partial",
		base + "not json\n", base + "[]\n", base + "null\n", base + "{} {}\n",
		base + "{\"uuid\":42}\n", base + "{\"uuid\":null}\n", base + "{\"uuid\":\"base\",\"changed\":true}\n",
		base + "{\"uuid\":\"one\",\"uuid\":\"two\"}\n", base + "{\"UUID\":\"alias\"}\n",
		base + "{\"uuid\":\"one\",\"extra\":1,\"extra\":2}\n",
		base + "{\"size\":0,\"clauderig_chunked_transcript\":1,\"parts\":[]}\n",
		base + "{\"CLAUDERIG_CHUNKED_TRANSCRIPT\":1}\n", base + "\x00\n", base + "\xff\n",
	} {
		for _, reverse := range []bool{false, true} {
			a, b := []byte(good), []byte(bad)
			if reverse {
				a, b = b, a
			}
			out, err := ResolveRetained(t.Context(), "cli/projects/p/s.jsonl", []byte(base), a, b)
			if !errors.Is(err, commitartifact.ErrConflict) || out != nil {
				t.Fatalf("accepted %q: %q %v", bad, out, err)
			}
		}
	}
	for _, path := range []string{"cli/settings.json", "cli/skills/readme.md", "cli/projects/p/s.jsonl.chunks/hash", "cli/history.jsonl", "cli/custom/events.jsonl", "desktop/audit.jsonl", "cli/projects/p/memory/data.jsonl", "cli/projects/p/s.JSONL", "cli/projects/p/s.jsonl.chunks/fake.jsonl"} {
		if _, err := ResolveRetained(t.Context(), path, []byte("base\n"), []byte("base\nours\n"), []byte("base\ntheirs\n")); !errors.Is(err, commitartifact.ErrConflict) {
			t.Fatal("unsupported file policy", path, err)
		}
	}
	for _, ancestor := range [][]byte{nil, []byte("unterminated")} {
		if _, err := ResolveRetained(t.Context(), "cli/projects/p/s.jsonl", ancestor, []byte(good), []byte(good)); !errors.Is(err, commitartifact.ErrConflict) {
			t.Fatal("unproven append accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := ResolveRetained(ctx, "cli/projects/p/s.jsonl", []byte(base), []byte(good), []byte(good)); !errors.Is(err, context.Canceled) || out != nil {
		t.Fatal("cancellation lost", err)
	}
}

func TestRetainedJSONWhitespace(t *testing.T) {
	base := []byte("{\"uuid\":\"base\"}\n")
	ours := append(bytes.Clone(base), []byte("{\"uuid\":\"ours\"}\n")...)
	for _, space := range []string{"\u00a0", "\u2003", "\v", "\f"} {
		for _, line := range []string{space + "{\"uuid\":\"remote\"}\n", "{\"uuid\":\"remote\"}" + space + "\n", space + "\n"} {
			theirs := append(bytes.Clone(base), []byte(line)...)
			out, err := ResolveRetained(t.Context(), "cli/projects/p/s.jsonl", base, ours, theirs)
			if !errors.Is(err, commitartifact.ErrConflict) || out != nil {
				t.Errorf("accepted non-JSON whitespace %q: %q %v", line, out, err)
			}
		}
	}
	// JSON whitespace stays byte-for-byte intact, and Unicode whitespace is
	// valid inside a JSON string even though it is invalid around the value.
	line := " \t{\"uuid\":\"remote\",\"text\":\"\u00a0\u2003\"}\t \r\n \t\r\n"
	theirs := append(bytes.Clone(base), []byte(line)...)
	out, err := ResolveRetained(t.Context(), "cli/projects/p/s.jsonl", base, ours, theirs)
	if err != nil || string(out) != string(ours)+line {
		t.Fatalf("changed valid JSON whitespace: %q %v", out, err)
	}
}

func TestRetainedPreservesSameSideDuplicates(t *testing.T) {
	base := "{\"uuid\":\"base\"}\n"
	local := "{\"uuid\":\"local\"}\n"
	remote := "{\"uuid\":\"remote\"}\n"
	ancestor := base + base
	ours, theirs := ancestor+local+local, ancestor+remote+remote+base+local
	got, err := ResolveRetained(t.Context(), "cli/projects/p/s/subagents/a.jsonl", []byte(ancestor), []byte(ours), []byte(theirs))
	if err != nil || string(got) != ours+remote+remote+base {
		t.Fatalf("changed same-side records: %q %v", got, err)
	}
}

func TestRetainedRejectsMixedCaseUUIDCollision(t *testing.T) {
	base := []byte("{}\n")
	ours := append(bytes.Clone(base), []byte("{\"uuid\":\"aaaaaaaa-0000-0000-0000-000000000000\"}\n")...)
	theirs := append(bytes.Clone(base), []byte("{\"uuid\":\"AAAAAAAA-0000-0000-0000-000000000000\"}\n")...)
	if _, err := ResolveRetained(t.Context(), "cli/projects/p/s.jsonl", base, ours, theirs); !errors.Is(err, commitartifact.ErrConflict) {
		t.Fatal("ambiguous UUID spelling accepted", err)
	}
}

func TestRetainedMachineJournalAppends(t *testing.T) {
	base := []byte("{\"at\":\"2026-09-09T00:00:00Z\",\"machine\":\"fixture\",\"op\":\"sync\",\"outcome\":\"ok\",\"future\":{\"keep\":true}}\r\n")
	left := []byte("{\"at\":\"2026-09-09T00:01:00Z\",\"machine\":\"fixture\",\"op\":\"sync\",\"outcome\":\"ok\"}\n")
	right := []byte("{\"at\":\"2026-09-09T00:02:00Z\",\"machine\":\"fixture\",\"op\":\"restore\",\"outcome\":\"failed\"}\n")
	for _, ancestor := range [][]byte{nil, {}, base} {
		ours := append(bytes.Clone(ancestor), left...)
		theirs := append(bytes.Clone(ancestor), right...)
		want := append(bytes.Clone(ours), right...)
		got, err := ResolveRetained(t.Context(), "journal/fixture.jsonl", ancestor, ours, theirs)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("journal append: %q %v", got, err)
		}
	}
	good := append(bytes.Clone(base), left...)
	for _, bad := range [][]byte{
		nil, left, append(bytes.Clone(base), []byte("not json\n")...),
		bytes.Replace(good, []byte(`"machine":"fixture"`), []byte(`"machine":"other"`), 1),
		bytes.Replace(good, []byte(`"op":"sync"`), []byte(`"op":"unknown"`), 1),
		bytes.Replace(good, []byte(`"outcome":"ok"`), []byte(`"outcome":"unknown"`), 1),
		append(bytes.Clone(base), []byte("{\"at\":\"2026-09-09T00:01:00Z\",\"machine\":\"fixture\",\"op\":\"sync\",\"outcome\":\"ok\",\"outcome\":\"failed\"}\n")...),
	} {
		if _, err := ResolveRetained(t.Context(), "journal/fixture.jsonl", base, good, bad); !errors.Is(err, commitartifact.ErrConflict) {
			t.Fatalf("accepted malformed/changed journal %q: %v", bad, err)
		}
	}
	for _, path := range []string{"journal/nested/fixture.jsonl", "journal/.jsonl", "cli/journal/fixture.jsonl", "journal/fixture.JSONL"} {
		if _, err := ResolveRetained(t.Context(), path, base, good, append(bytes.Clone(base), right...)); !errors.Is(err, commitartifact.ErrConflict) {
			t.Fatal("unexpected journal policy", path, err)
		}
	}
}
