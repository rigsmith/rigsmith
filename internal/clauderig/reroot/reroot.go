// Package reroot works out where a session's work actually happened, which is
// often not where it was launched.
//
// A session is filed under the directory Claude Code started in, and the agent
// then goes wherever the work is. On a real machine 65 of 120 recent transcripts
// recorded more than one cwd. The result is a chat filed under ~/Git — a folder
// nobody works in — when everything it did was in one project underneath.
package reroot

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

// FiledShareCeiling is how much of a transcript has to happen somewhere OTHER
// than the filed directory before a move is worth suggesting.
//
// Tuned against real sessions rather than picked. A session that spent 97% of
// its records in its own directory and 3% in two build folders has not moved
// anywhere; one that spent half its records in a project underneath the launch
// directory has.
const FiledShareCeiling = 0.6

// Analysis is where a session was filed against where it worked.
type Analysis struct {
	Filed string `json:"filed"`
	// Counts is records per directory, so a caller can show its working.
	Counts map[string]int `json:"counts,omitempty"`
	// FiledShare is the fraction of records that stayed in the filed directory.
	FiledShare float64 `json:"filedShare"`
	// Suggested is where the session looks like it belongs, empty when it is
	// already filed correctly or when the answer is genuinely ambiguous.
	Suggested string `json:"suggested,omitempty"`
	// Reason says why, in the terms the decision was made in.
	Reason string `json:"reason,omitempty"`
	Total  int    `json:"total"`
}

// Drifted reports whether there is a move worth offering.
func (a Analysis) Drifted() bool { return a.Suggested != "" && a.Suggested != a.Filed }

// Analyze reads a transcript and decides where it belongs.
//
// The rule is deliberately conservative, because the failure it guards against
// is offering to re-file a session that is fine:
//
//   - If most records stayed in the filed directory, the session is where it
//     belongs and wandering into a build folder proves nothing.
//   - Otherwise the directories it actually worked in must share a common
//     ancestor DEEPER than the filed one. That is the difference between "was
//     launched in ~/Git and lives in ~/Git/githail" and "was launched in ~/Git
//     and genuinely touched three projects", where the launch directory is the
//     honest answer and no move should be offered.
//
// MinHeld is how many distinct projects a directory has to hold before it counts
// as a container rather than a place work is done.
const MinHeld = 3

// Containers finds the directories that merely HOLD projects, by asking the
// sessions themselves: a directory that several distinct projects sit directly
// inside is somewhere you cd through, not somewhere you work.
//
// Derived from the tree rather than guessed at from the filesystem. The obvious
// test — does it contain a .git — is wrong in both directions: it excludes a
// plain folder that is unmistakably a project (the case this whole feature
// exists for was a directory with no repository in it at all), and it includes
// every checkout that happens to sit under another one.
//
// On a real machine this finds ~/Git holding 23 projects and two worktree roots
// holding 56 and 16, and no repository at all — which is the shape wanted.
func Containers(filedCwds []string) map[string]bool {
	held := map[string]map[string]bool{}
	for _, cwd := range filedCwds {
		parent := path.Dir(path.Clean(cwd))
		if parent == "" || parent == "/" || parent == "." {
			continue
		}
		if held[parent] == nil {
			held[parent] = map[string]bool{}
		}
		held[parent][cwd] = true
	}
	out := map[string]bool{}
	for dir, kids := range held {
		if len(kids) >= MinHeld {
			out[dir] = true
		}
	}
	return out
}

// Analyze decides where one session belongs. containers comes from [Containers]
// over every filed directory on the machine.
func Analyze(transcriptPath, filed string, containers map[string]bool) (Analysis, error) {
	return analyze(transcriptPath, filed, func(d string) bool { return !containers[d] })
}

func analyze(transcriptPath, filed string, isRoot func(string) bool) (Analysis, error) {
	a := Analysis{Filed: filed, Counts: map[string]int{}}

	f, err := os.Open(transcriptPath)
	if err != nil {
		return a, err
	}
	defer f.Close()

	// bufio.Reader, not Scanner: one transcript line can exceed any token limit
	// Scanner accepts, and a dropped line is a dropped vote.
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, rerr := r.ReadBytes('\n')
		if len(line) > 0 {
			var probe struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(line, &probe) == nil && probe.Cwd != "" {
				a.Counts[probe.Cwd]++
				a.Total++
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return a, rerr
		}
	}
	if a.Total == 0 {
		return a, nil
	}
	a.FiledShare = float64(a.Counts[filed]) / float64(a.Total)

	if a.FiledShare > FiledShareCeiling {
		a.Reason = "most of the work happened in the directory it is filed under"
		return a, nil
	}

	elsewhere := make([]string, 0, len(a.Counts))
	for dir := range a.Counts {
		if dir != filed {
			elsewhere = append(elsewhere, dir)
		}
	}
	if len(elsewhere) == 0 {
		return a, nil
	}
	sort.Strings(elsewhere)

	common := commonDir(elsewhere)
	switch {
	case common == "" || common == "/":
		a.Reason = "the work was spread across unrelated directories"
	case common == filed || !strings.HasPrefix(common, strings.TrimSuffix(filed, "/")+"/"):
		// Not deeper than where it already is: the launch directory is the
		// honest common ancestor, and re-filing would say something untrue.
		a.Reason = "the work spans several projects under the directory it is filed in"
	case isRoot(filed):
		// Depth alone is not drift. Wandering from a project into ui/ or src/ or
		// a build folder is what working in a project looks like, and the project
		// is the unit a session belongs to. Over 688 real transcripts this one
		// condition is the difference between 49 suggestions — most of them
		// re-filing a project under its own subfolder — and the few that are
		// genuinely misfiled.
		a.Reason = "already filed under a project; working in its subfolders is not drift"
	default:
		a.Suggested = common
		a.Reason = "launched in a directory that holds projects, but the work was all in one of them"
	}
	return a, nil
}

// commonDir is the deepest directory every path is at or under.
func commonDir(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	parts := strings.Split(path.Clean(dirs[0]), "/")
	for _, d := range dirs[1:] {
		other := strings.Split(path.Clean(d), "/")
		n := 0
		for n < len(parts) && n < len(other) && parts[n] == other[n] {
			n++
		}
		parts = parts[:n]
	}
	out := strings.Join(parts, "/")
	if out == "" {
		return "/"
	}
	return out
}
