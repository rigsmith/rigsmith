package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/sessions"
)

// checkSessionFiling reports sessions filed under more than one project
// directory, and Desktop sidecars naming a directory that no longer holds the
// transcript.
//
// Detection is in the sessions package rather than here: the dashboard and the
// window report the same thing, and three implementations of "is this session
// filed correctly" would eventually disagree about it.
//
// Reported, never auto-fixed. Which copy of a split session is wanted is usually
// obvious and occasionally not — when two have genuinely diverged, discarding
// either loses a conversation — so there is no Fix here on purpose.
func checkSessionFiling(_ context.Context, env Env) Result {
	const id, name = "session-filing", "session filing"
	if env.Cfg == nil {
		return Result{ID: id, Name: name, Status: Info, Detail: "no config"}
	}
	home, _ := env.Cfg.RootLocation("cli", env.Machine)
	if home == "" {
		return Result{ID: id, Name: name, Status: Info, Detail: "no ~/.claude on this machine"}
	}
	h := sessions.CheckHealth(
		[]search.Target{{Label: sessions.CLISource, Dir: home}},
		sessions.Roots(env.Cfg, env.Machine, false, false),
	)
	if h.OK() {
		return Result{ID: id, Name: name, Status: OK, Detail: "every session is filed in one place"}
	}

	var b strings.Builder
	if n := len(h.Splits); n > 0 {
		fmt.Fprintf(&b, "%s filed in more than one project directory", countOf(n, "session", "sessions"))
		// Named with their slugs: an id alone says nothing about where to look.
		for i, s := range h.Splits {
			if i == 3 {
				fmt.Fprintf(&b, "\n    … and %d more", n-3)
				break
			}
			fmt.Fprintf(&b, "\n    %s  also in %s", s.ID[:8], strings.Join(slugsOf(s.Others), ", "))
		}
	}
	if n := len(h.Stale); n > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s naming a directory that no longer holds the transcript",
			countOf(n, "Desktop sidecar", "Desktop sidecars"))
		for i, s := range h.Stale {
			if i == 3 {
				fmt.Fprintf(&b, "\n    … and %d more", n-3)
				break
			}
			fmt.Fprintf(&b, "\n    %s  says %s", s.ID[:8], s.Says)
		}
	}
	return Result{
		ID: id, Name: name, Status: Warn, Detail: b.String(),
		Hint: "Claude Desktop opens the copy in the directory its sidecar names, which may be an " +
			"older one — `clauderig reroot <session> <dir>` re-files a session where the work is",
	}
}

// slugsOf reduces transcript paths to the project directories holding them.
func slugsOf(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, projectSlug(p))
	}
	return out
}

// projectSlug is the directory a transcript sits in.
func projectSlug(path string) string { return filepath.Base(filepath.Dir(path)) }

func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
