package cli

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// killCandidate is one process the kill sweep matched: its PID and a label (the
// command line) shown in the review picker.
type killCandidate struct {
	pid   int
	label string
}

// killReview shows the matched processes in a pre-checked multi-select, then
// kills the ones the user keeps. Used on an interactive terminal (the safe
// default); --yes and non-TTY runs kill every match without asking.
func killReview(cmd *cobra.Command, out io.Writer, root string, cands []killCandidate) error {
	if len(cands) == 0 {
		fmt.Fprintln(out, dimStyle.Render("no matching processes"))
		return nil
	}
	pids, ok := pickKillTargets(cands)
	if !ok {
		fmt.Fprintln(out, dimStyle.Render("kill cancelled"))
		return nil
	}
	if len(pids) == 0 {
		fmt.Fprintln(out, dimStyle.Render("nothing selected — killed nothing"))
		return nil
	}
	killed := killPids(cmd, root, pids)
	fmt.Fprintf(out, "killed %d process(es)\n", killed)
	return nil
}

// pickKillTargets shows the candidates pre-checked; the user unchecks any to
// spare, then confirms. Returns the selected PIDs, or ok=false on esc/ctrl+c.
func pickKillTargets(cands []killCandidate) (pids []int, ok bool) {
	var selected []int
	opts := make([]huh.Option[int], 0, len(cands))
	for _, c := range cands {
		opts = append(opts, huh.NewOption(fmt.Sprintf("%d  %s", c.pid, truncateLabel(c.label, 90)), c.pid).Selected(true))
	}
	ms := huh.NewMultiSelect[int]().
		Title("Kill which processes? (space toggles · enter confirms · esc cancels)").
		Options(opts...).
		Value(&selected)
	if err := runHuhMultiSelect(ms); err != nil {
		return nil, false
	}
	return selected, true
}

// patternCandidatesPosix enumerates the processes matching the patterns via
// `pgrep -fl` (PID + command line), deduped by PID and excluding rig itself.
func patternCandidatesPosix(cmd *cobra.Command, root string, patterns []string) []killCandidate {
	self := os.Getpid()
	seen := map[int]bool{}
	var cands []killCandidate
	for _, pattern := range patterns {
		for _, line := range nonEmptyLines(captureOut(cmd, root, "pgrep", "-fl", pattern)) {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			pid, err := strconv.Atoi(fields[0])
			if err != nil || pid <= 0 || pid == self || seen[pid] {
				continue
			}
			seen[pid] = true
			label := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
			cands = append(cands, killCandidate{pid: pid, label: label})
		}
	}
	return cands
}

// portCandidates labels the given PIDs (resolved from listening ports) with
// their command lines for the review picker.
func portCandidates(cmd *cobra.Command, root string, pids []int) []killCandidate {
	labels := pidLabels(cmd, root, pids)
	cands := make([]killCandidate, 0, len(pids))
	for _, pid := range pids {
		label := labels[pid]
		if label == "" {
			label = fmt.Sprintf("(pid %d)", pid)
		}
		cands = append(cands, killCandidate{pid: pid, label: label})
	}
	return cands
}

// pidLabels resolves each PID to its command line. POSIX uses `ps -o pid=,args=`;
// Windows pulls from the CIM process list (best-effort).
func pidLabels(cmd *cobra.Command, root string, pids []int) map[int]string {
	out := map[int]string{}
	if len(pids) == 0 {
		return out
	}
	if runtime.GOOS == "windows" {
		if procs, ok := windowsProcessList(cmd, root); ok {
			for _, p := range procs {
				out[p.Pid] = p.CommandLine
			}
		}
		return out
	}
	text := captureOut(cmd, root, "ps", "-o", "pid=,args=", "-p", joinInts(pids, ","))
	for _, line := range nonEmptyLines(text) {
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		out[pid] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
	}
	return out
}

// truncateLabel shortens a command line to max runes, with an ellipsis.
func truncateLabel(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
