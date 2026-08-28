package reroot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// transcript writes a .jsonl whose records carry the given cwds, n times each.
func transcript(t *testing.T, counts map[string]int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for cwd, n := range counts {
		for i := 0; i < n; i++ {
			if err := enc.Encode(map[string]any{"type": "user", "cwd": cwd}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return p
}

// The three shapes below are taken from real transcripts on a working machine.
// A naive "most common cwd" gets two of the three wrong, which is why the rule
// is not that.
func TestAnalyze_RealShapes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		filed     string
		counts    map[string]int
		holders   []string // directories that merely HOLD projects
		suggested string
	}{
		{
			// Launched in ~/Git, all the work in one project underneath. This is
			// the case worth offering to fix.
			name:  "launched a level too high",
			filed: "/Users/john/Git",
			counts: map[string]int{
				"/Users/john/Git":             1780,
				"/Users/john/Git/githail":     1278,
				"/Users/john/Git/githail/app": 466,
			},
			holders:   []string{"/Users/john/Git"},
			suggested: "/Users/john/Git/githail",
		},
		{
			// Genuinely spanned two sibling projects. The launch directory IS
			// the honest answer; re-filing under either sibling would be a lie.
			name:  "genuinely multi-project",
			filed: "/Users/john/Git",
			counts: map[string]int{
				"/Users/john/Git":               2992,
				"/Users/john/Git/xtermnet-perf": 3482,
				"/Users/john/Git/term-perf":     2287,
			},
			holders:   []string{"/Users/john/Git"},
			suggested: "",
		},
		{
			// Filed correctly and merely wandered into its own build output.
			// Most common cwd is right here, but the deepest common ancestor of
			// the others is not — hence the share check comes first.
			name:  "wandered into subdirectories",
			filed: "/Users/john/Git/porta-pty",
			counts: map[string]int{
				"/Users/john/Git/porta-pty":                         2908,
				"/Users/john/Git/porta-pty/src/Porta.Pty.Native":    38,
				"/Users/john/Git/porta-pty/src/Porta.Pty.Tests/bin": 35,
			},
			holders:   nil,
			suggested: "",
		},
		{
			// Filed under a repository and the work drifted into ui/. The
			// repository is the unit a session belongs to; re-filing it under
			// its own subfolder is the mistake this rule exists to avoid, and
			// it accounted for most of 49 suggestions on a real machine.
			name:  "drifted within its own repository",
			filed: "/Users/john/Git/telegram",
			counts: map[string]int{
				"/Users/john/Git/telegram":    300,
				"/Users/john/Git/telegram/ui": 1200,
			},
			holders:   nil,
			suggested: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			holders := map[string]bool{}
			for _, h := range tc.holders {
				holders[h] = true
			}
			got, err := Analyze(transcript(t, tc.counts), tc.filed, holders)
			if err != nil {
				t.Fatal(err)
			}
			if got.Suggested != tc.suggested {
				t.Errorf("Suggested = %q, want %q (reason: %s)", got.Suggested, tc.suggested, got.Reason)
			}
			if got.Drifted() != (tc.suggested != "") {
				t.Errorf("Drifted() = %v", got.Drifted())
			}
			if got.Reason == "" {
				t.Error("no reason given — the suggestion has to be explicable")
			}
		})
	}
}

func TestAnalyze_SingleDirectorySessionIsLeftAlone(t *testing.T) {
	got, err := Analyze(transcript(t, map[string]int{"/Users/john/Git/p": 500}), "/Users/john/Git/p", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Drifted() {
		t.Errorf("a session that never left its directory was flagged: %+v", got)
	}
}

// An empty or unreadable transcript must produce no suggestion rather than a
// confident wrong one.
func TestAnalyze_EmptyTranscript(t *testing.T) {
	got, err := Analyze(transcript(t, nil), "/Users/john/Git/p", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Drifted() || got.Total != 0 {
		t.Errorf("empty transcript produced %+v", got)
	}
}

func TestCommonDir(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"/a/b/c", "/a/b/d"}, "/a/b"},
		{[]string{"/a/b", "/a/b"}, "/a/b"},
		{[]string{"/a/b", "/c/d"}, "/"},
		{[]string{"/a/b/c"}, "/a/b/c"},
	} {
		if got := commonDir(tc.in); got != tc.want {
			t.Errorf("commonDir(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A directory several distinct projects sit directly inside is somewhere you cd
// through, not somewhere you work. Asking the sessions beats guessing from the
// filesystem: the case this feature exists for was a project folder with no
// repository in it at all.
func TestContainers(t *testing.T) {
	got := Containers([]string{
		"/Users/john/Git/a", "/Users/john/Git/b", "/Users/john/Git/c",
		"/Users/john/Git/a/ui", // one child is not a container
		"/Users/john/solo/only",
	})
	if !got["/Users/john/Git"] {
		t.Error("a directory holding three projects was not recognised as a container")
	}
	if got["/Users/john/Git/a"] {
		t.Error("a project with one subfolder was called a container")
	}
	if got["/Users/john/solo"] {
		t.Error("a directory holding one project was called a container")
	}
}
