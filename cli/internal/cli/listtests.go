// Accurate .NET test enumeration via the platform's own discovery
// (`dotnet test --list-tests`), runner-aware (MTP vs VSTest). This replaces a
// best-effort source scan as the primary source of test-class names for
// `rig test <query>`: the platform's adapters discover every framework's tests
// (xUnit/NUnit/MSTest/TUnit, theories, inherited fixtures) by construction, so
// the candidate list no longer depends on a hardcoded attribute set. The scan
// (discoverTestClassNames) remains the fallback when listing fails (e.g. a
// broken build), and `rig test` itself still hands the resolved `--filter` to
// the platform, so nothing is ever silently skipped.
package cli

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

var spinnerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14")) // cyan

// listTestClassNames asks the .NET test platform to enumerate the test project
// and returns the discovered test classes' fully-qualified names (sorted,
// deduped). Returns nil if listing fails so the caller can fall back to the
// source scan; a build/discovery pass runs under a spinner since it can take a
// few seconds.
func listTestClassNames(cmd *cobra.Command, root string, runner dotnetTestRunner, testProject string) []string {
	argv := buildListTestsArgs(runner, testProject)
	out, err := captureWithSpinner(cmd, root, "Discovering tests (dotnet test --list-tests)", "dotnet", argv...)
	if err != nil {
		return nil
	}
	return parseListedTestClasses(out)
}

// buildListTestsArgs is the `dotnet test … --list-tests` argument list. The
// project arg form follows the runner's CLI grammar (VSTest positional, MTP
// `--project`), mirroring buildDotnetTestArgs. Pure.
func buildListTestsArgs(runner dotnetTestRunner, testProject string) []string {
	if runner == mtpRunner {
		return []string{"test", "--project", testProject, "--list-tests"}
	}
	return []string{"test", testProject, "--list-tests"}
}

// listedTestToken matches a fully-qualified test identifier (Namespace…Class
// .Method, '+' for nested types) and nothing else — so build/log/banner lines
// are rejected.
var listedTestToken = regexp.MustCompile(`^[A-Za-z_][\w.+]*$`)

// parseListedTestClasses extracts test-class FQNs from `--list-tests` output.
// It reads each line's leading token (up to whitespace or '(' — VSTest indents
// names; theory rows append data args), keeps the ones shaped like a test FQN,
// and drops the trailing method segment to get the class. Nested-type '+'
// separators are normalized to '.'. Format-agnostic: VSTest prints fully-
// qualified `Namespace.Class.Method` names (extracted); MTP runners that list
// only display names (e.g. MSTest prints "Builds", "Works") yield nothing here,
// so the caller falls back to the source scan. Pure.
func parseListedTestClasses(output string) []string {
	seen := map[string]bool{}
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		token := line
		if i := strings.IndexAny(token, " \t("); i >= 0 {
			token = token[:i]
		}
		if !strings.Contains(token, ".") || !listedTestToken.MatchString(token) {
			continue
		}
		token = strings.ReplaceAll(token, "+", ".")
		// Drop the method segment; what remains is the class FQN.
		class := token[:strings.LastIndex(token, ".")]
		if class != "" {
			seen[class] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// captureWithSpinner runs argv in dir, capturing stdout, while showing a
// spinner with label on stderr so the user knows it's waiting on dotnet. The
// spinner only animates on a TTY (and not under --quiet); otherwise it degrades
// to a single status line (or silence under --quiet). The subprocess runs in a
// goroutine; the animation loop ends as soon as it returns.
func captureWithSpinner(cmd *cobra.Command, dir, label, name string, args ...string) (string, error) {
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := capture(cmd, dir, name, args...)
		done <- result{out, err}
	}()

	w := cmd.ErrOrStderr()
	if quiet || !term.IsTerminal(os.Stderr.Fd()) {
		if !quiet {
			fmt.Fprintln(w, dimStyle.Render(label+"…"))
		}
		r := <-done
		return r.out, r.err
	}

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	clear := "\r" + strings.Repeat(" ", lipgloss.Width(label)+4) + "\r"
	for i := 0; ; i++ {
		select {
		case r := <-done:
			fmt.Fprint(w, clear)
			return r.out, r.err
		case <-ticker.C:
			fmt.Fprintf(w, "\r%s %s", spinnerStyle.Render(frames[i%len(frames)]), dimStyle.Render(label))
		}
	}
}
