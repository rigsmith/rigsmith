// Package cliguard checks a cobra command tree against rigsmith's cross-tool CLI
// conventions — the consistency rules we used to enforce by hand-auditing every
// new command. It is the Go answer to "something like dependency-cruiser, but for
// our command surface": construct each tool's root, walk it, and assert the
// conventions in a test. Violations are returned as data, so a run can list them
// all in report-only mode before the gate is flipped to hard-fail in CI.
//
// What it does NOT do: cobra already rejects duplicate shorthands within a single
// command at registration time, so that case can't reach a built tree. cliguard
// covers the cross-command conventions cobra has no opinion on — reserved
// shorthands, the list-subcommand shape, doctor --fix, and bare command groups
// that should open a menu.
package cliguard

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Violation is a single convention breach at a specific command path.
type Violation struct {
	Tool   string // root command name (rig / shiprig / changerig / clauderig)
	Path   string // full command path, e.g. "rig worktree new"
	Rule   string // short rule id, e.g. "reserved-shorthand"
	Detail string // human-readable explanation
}

func (v Violation) String() string {
	return fmt.Sprintf("[%s] %s — %s", v.Rule, v.Path, v.Detail)
}

// reserved maps a canonical long flag name to the single shorthand letter it must
// use everywhere — and, reversed, reserves that letter so it can never mean
// anything else across any tool. These are the universal flags whose meaning must
// not drift between commands. Local mnemonics (-t type/transport, -s scope,
// -o output, -p package, …) are intentionally left free per command.
var reserved = map[string]string{
	"dry-run":     "n",
	"yes":         "y",
	"force":       "f",
	"interactive": "i",
	"kill":        "k",
	"all":         "a",
	"watch":       "w",
	"message":     "m",
}

// cobraOwned are shorthands cobra itself manages (help, version). They are never
// flagged, since tools don't define them.
var cobraOwned = map[string]bool{"h": true, "v": true}

// letterOwners inverts reserved: shorthand → the only long name allowed to use it.
func letterOwners() map[string]string {
	m := make(map[string]string, len(reserved))
	for name, letter := range reserved {
		m[letter] = name
	}
	return m
}

// Check walks the full command tree rooted at root and returns every convention
// violation it finds. The root's name is recorded as the tool on each violation.
func Check(root *cobra.Command) []Violation {
	owners := letterOwners()
	var out []Violation
	walk(root, func(cmd *cobra.Command) {
		out = append(out, checkCommand(root.Name(), root, cmd, owners)...)
	})
	return out
}

// walk visits cmd and every descendant (depth-first), skipping cobra's
// auto-generated help/completion commands.
func walk(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, c := range cmd.Commands() {
		if c.Name() == "help" || c.Name() == "completion" || c.Hidden {
			continue
		}
		walk(c, fn)
	}
}

func checkCommand(tool string, root, cmd *cobra.Command, owners map[string]string) []Violation {
	var out []Violation
	path := cmd.CommandPath()

	// Flag conventions: only inspect flags defined ON this command (LocalFlags
	// excludes inherited persistent flags), so a root persistent flag like
	// `--dry-run -n` is checked once, not once per subcommand.
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		name, sh := f.Name, f.Shorthand
		if name == "help" || name == "version" {
			return
		}
		// reserved-shorthand: a canonical flag name must carry its reserved letter.
		if want, ok := reserved[name]; ok && sh != want {
			got := "no shorthand"
			if sh != "" {
				got = "-" + sh
			}
			out = append(out, Violation{tool, path, "reserved-shorthand",
				fmt.Sprintf("--%s must use -%s (has %s)", name, want, got)})
		}
		// reserved-letter: a reserved letter must only ever mean its canonical name.
		if sh != "" && !cobraOwned[sh] {
			if owner, ok := owners[sh]; ok && owner != name {
				out = append(out, Violation{tool, path, "reserved-letter",
					fmt.Sprintf("-%s is reserved for --%s, used here for --%s", sh, owner, name)})
			}
		}
		// list-flag: listing is a `list` subcommand, not a boolean --list flag.
		if name == "list" {
			out = append(out, Violation{tool, path, "list-flag",
				"replace --list with a `list` subcommand (alias `ls`)"})
		}
	})

	// doctor-fix: every `doctor` command exposes a --fix flag.
	if cmd.Name() == "doctor" && cmd.Flags().Lookup("fix") == nil {
		out = append(out, Violation{tool, path, "doctor-fix", "doctor command is missing a --fix flag"})
	}

	// group-menu: a command that only groups subcommands (no RunE/Run of its own)
	// should open an interactive menu on a TTY. The root is exempt — each tool
	// routes bare invocation to its `ui` verb outside the tree. This rule's output
	// is the running list of groups still to be wired to a menu.
	if cmd != root && len(childCommands(cmd)) > 0 && cmd.RunE == nil && cmd.Run == nil {
		out = append(out, Violation{tool, path, "group-menu",
			"command group has no RunE — bare invocation won't open a menu"})
	}

	return out
}

// childCommands returns cmd's real subcommands, excluding cobra's generated ones.
func childCommands(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "help" || c.Name() == "completion" || c.Hidden {
			continue
		}
		out = append(out, c)
	}
	return out
}

// Report groups violations by rule into a stable, readable block for test output.
func Report(vs []Violation) string {
	byRule := map[string][]Violation{}
	var rules []string
	for _, v := range vs {
		if _, ok := byRule[v.Rule]; !ok {
			rules = append(rules, v.Rule)
		}
		byRule[v.Rule] = append(byRule[v.Rule], v)
	}
	sort.Strings(rules)
	var b strings.Builder
	for _, r := range rules {
		items := byRule[r]
		sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
		fmt.Fprintf(&b, "  %s (%d)\n", r, len(items))
		for _, v := range items {
			fmt.Fprintf(&b, "    • %-28s %s\n", v.Path, v.Detail)
		}
	}
	return b.String()
}
