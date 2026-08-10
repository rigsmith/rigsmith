// Package cmderr builds the message attached to a failed command's error.
//
// Every ecosystem adapter shells out to a package manager and captures both of
// its streams. Wrapping only stderr loses the diagnosis whenever the tool talks
// on stdout instead — and several do: `dotnet nuget push` reports "warn : No API
// Key was provided" and "error: … 403 (Forbidden)" there with stderr empty, and
// `vpk` writes its fatal line the same way. The failure then surfaces as an exit
// code, a colon, and nothing, and the only way to learn the reason is to re-run
// the command by hand.
//
// Detail is the shared answer, so an adapter reports what its tool actually said
// no matter which stream it chose.
package cmderr

import "strings"

// TailLines is how many trailing lines of stdout an error carries: enough for a
// build tool's error summary, short of its whole log.
const TailLines = 20

// Detail is the text to attach to a failed command's error: stderr, followed by
// the tail of stdout. Both are included rather than one preferred, since a tool
// can split a failure across them — a generic line on stderr, the reason on
// stdout. The stdout side is bounded (see TailLines) because build tools print
// a full log ahead of the summary that ends it. A command that failed silently
// yields "(no output)", so the error reads as a sentence instead of trailing
// off after the colon.
func Detail(stdout, stderr string) string {
	detail := strings.TrimSpace(stderr)
	if tail := LastLines(strings.TrimSpace(stdout), TailLines); tail != "" {
		if detail != "" {
			detail += "\n" + tail
		} else {
			detail = tail
		}
	}
	if detail == "" {
		return "(no output)"
	}
	return detail
}

// LastLines returns the final n lines of s (all of it when it has n or fewer) —
// the end of a command's output, where its failure is reported.
func LastLines(s string, n int) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
