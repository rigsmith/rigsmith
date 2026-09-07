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
		{"identical uuid", "cli/history.JSONL", base, base + ours + common, base + common + theirs, base + ours + common + theirs},
		{"unkeyed records", "cli/history.jsonl", base, base + ours + unkeyed, base + theirs + unkeyed, base + ours + unkeyed + theirs + unkeyed},
		{"empty base", "cli/history.jsonl", "", ours, theirs, ours + theirs},
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
	for _, path := range []string{"cli/settings.json", "cli/skills/readme.md", "cli/projects/p/s.jsonl.chunks/hash"} {
		if _, err := ResolveRetained(t.Context(), path, []byte("base\n"), []byte("base\nours\n"), []byte("base\ntheirs\n")); !errors.Is(err, commitartifact.ErrConflict) {
			t.Fatal("unsupported file policy", path, err)
		}
	}
	for _, ancestor := range [][]byte{nil, []byte("unterminated")} {
		if _, err := ResolveRetained(t.Context(), "cli/history.jsonl", ancestor, []byte(good), []byte(good)); !errors.Is(err, commitartifact.ErrConflict) {
			t.Fatal("unproven append accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := ResolveRetained(ctx, "cli/history.jsonl", []byte(base), []byte(good), []byte(good)); !errors.Is(err, context.Canceled) || out != nil {
		t.Fatal("cancellation lost", err)
	}
}
