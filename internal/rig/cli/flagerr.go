package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// forwardsAnnotation marks a verb that appends whatever it doesn't consume to
// the command it runs — the dev verbs, the dependency verbs, dlx. It is what
// lets an unknown flag promise that `--` fixes it: on a verb that assembles its
// own argv (coverage, publish) the same line would fail on arg count instead,
// so those stay unannotated and keep pflag's plain error.
const forwardsAnnotation = "rigForwards"

// markForwards records that cmd forwards its extra args to the underlying
// command, and states the `--` convention in its help. Both come from one call
// so a new forwarding verb can't document the escape hatch without also being
// able to suggest it (or the reverse). example is the `rig … -- …` line shown
// under the verb's usage.
func markForwards(cmd *cobra.Command, example string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[forwardsAnnotation] = "1"
	if cmd.Example == "" {
		cmd.Example = "# flags rig doesn't own go to the underlying command after --\n" + example
	}
	return cmd
}

// forwardsArgs reports whether cmd passes its extra args through to the command
// it runs.
func forwardsArgs(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Annotations[forwardsAnnotation] != ""
}

// argsBeforeDash returns the args the user typed ahead of `--`, i.e. the ones a
// verb may read as a project name or a filter. Everything after the separator
// was written for the underlying command and must never be interpreted, so a
// forwarded flag can't be mistaken for a selector. With no `--` present cobra
// reports -1 and every arg is a candidate.
func argsBeforeDash(cmd *cobra.Command, args []string) []string {
	if n := cmd.ArgsLenAtDash(); n >= 0 && n <= len(args) {
		return args[:n]
	}
	return args
}

// processArgs is the command line the unknown-flag hint quotes back — the real
// one, so the suggested fix is the user's own line with `--` inserted rather
// than a generic example. A var so tests can supply one.
var processArgs = func() []string {
	if len(os.Args) == 0 {
		return nil
	}
	return os.Args[1:]
}

// flagErrorHint is rig's cobra FlagErrorFunc (set on the root, inherited by
// every subcommand). pflag's own message names the flag it rejected and stops
// there, which leaves two questions unanswered: whether it was a typo of a flag
// rig does have, and how to reach the underlying tool with a flag rig will
// never have. Both answers are known here — the command's flag set, and the
// argv that produced the error — so the error carries them. Errors that aren't
// about an unknown flag (a missing value, a bad type) pass through untouched.
func flagErrorHint(cmd *cobra.Command, err error) error {
	name, token, ok := unknownFlag(err)
	if !ok {
		return err
	}
	hint := &unknownFlagError{path: cmd.CommandPath(), name: name, nearest: nearestFlag(cmd, name)}
	if forwardsArgs(cmd) {
		hint.fix = passthroughLine(cmd, token)
	}
	if hint.nearest == "" && hint.fix == "" {
		return err // nothing to add — don't restate pflag's message at length
	}
	return hint
}

// unknownFlagError is an unknown flag reported with the two things that
// actually unblock the user: the flag rig probably meant, and the same command
// line rewritten to forward the flag past `--`.
//
// Its message is deliberately multi-line — a headline, then a paragraph, then
// the fix on its own indented line so it can be copied as typed. core/fang
// renders the headline as the error sentence and leaves the rest unwrapped.
type unknownFlagError struct {
	path    string // command path, e.g. "rig build"
	name    string // the rejected flag as it should be spelled, e.g. "--target"
	nearest string // the closest flag the command does have, if any
	fix     string // the user's command line with `--` inserted, if the verb forwards
}

func (e *unknownFlagError) Error() string {
	// The "unknown flag: " prefix is load-bearing: core/fang keys its "Try
	// --help for usage" line off it, as cobra's own usage errors do.
	msg := fmt.Sprintf("unknown flag: %s\n\n%s doesn't take %s.", e.name, e.path, e.name)
	if e.nearest != "" {
		msg += fmt.Sprintf(" Did you mean %s?", e.nearest)
	}
	if e.fix != "" {
		lead := " To pass it to the underlying command, put it after --:"
		if e.nearest != "" {
			lead = " To pass it to the underlying command instead, put it after --:"
		}
		msg += lead + "\n\n    " + e.fix
	}
	return msg
}

// unknownFlag pulls the rejected flag out of a pflag parse error: name is how
// it should be written back to the user ("--target", "-x"), token is what to
// look for in argv — for a long flag the same string (an "=value" form matches
// by prefix), for a shorthand the whole cluster the letter came from, since
// that is the argument `--` has to move past. ok is false for every other flag
// error, which pflag words differently and rig has nothing to add to.
func unknownFlag(err error) (name, token string, ok bool) {
	msg := err.Error()
	if rest, found := strings.CutPrefix(msg, "unknown flag: "); found {
		return rest, rest, true
	}
	// pflag: `unknown shorthand flag: 'x' in -xyz`.
	if rest, found := strings.CutPrefix(msg, "unknown shorthand flag: "); found {
		letter, cluster, split := strings.Cut(rest, " in ")
		if !split {
			return "", "", false
		}
		return "-" + strings.Trim(letter, "'"), cluster, true
	}
	return "", "", false
}

// passthroughLine rewrites the user's own command line to forward the rejected
// flag: the same tokens, with `--` inserted immediately before the one that
// failed. Everything from there on goes to the underlying command, so a line
// carrying several unknown flags is fixed by one edit rather than one per flag.
// The result is meant to be copied verbatim, so args that need quoting get it.
func passthroughLine(cmd *cobra.Command, token string) string {
	args := processArgs()
	for i, arg := range args {
		keep, forward, ok := splitAtFlag(arg, token)
		if !ok {
			continue
		}
		line := make([]string, 0, len(args)+3)
		line = append(line, cmd.Root().Name())
		line = append(line, quoteAll(args[:i])...)
		if keep != "" {
			line = append(line, keep)
		}
		line = append(line, "--", shellArg(forward))
		line = append(line, quoteAll(args[i+1:])...)
		return strings.Join(line, " ")
	}
	// The parser saw the token, so this shouldn't happen (a shell that rewrote
	// it, a test with a stubbed argv). Fall back to the shortest true statement
	// rather than quote a line the user didn't type.
	return cmd.CommandPath() + " -- " + token
}

// splitAtFlag decides where `--` goes within a single argument: keep is the
// part that stays in front of the separator, forward the part that moves behind
// it, ok whether this argument is the one that failed at all.
//
// Usually the whole argument moves. The exception is a shorthand cluster, where
// one token can hold flags rig understood and one it didn't: pflag reports the
// letters from the failure on ("-Z" of "-aZ"), so the leading letters keep
// their meaning and only the tail is forwarded.
func splitAtFlag(arg, token string) (keep, forward string, ok bool) {
	if arg == token || strings.HasPrefix(arg, token+"=") {
		return "", arg, true
	}
	letters, isShorthand := strings.CutPrefix(token, "-")
	if !isShorthand || strings.HasPrefix(token, "--") || letters == "" {
		return "", "", false
	}
	if strings.HasPrefix(arg, "--") || !strings.HasPrefix(arg, "-") {
		return "", "", false
	}
	if !strings.HasSuffix(arg, letters) || len(arg) <= len(letters)+1 {
		return "", "", false
	}
	return arg[:len(arg)-len(letters)], token, true
}

// quoteAll shell-quotes each arg that needs it, so the suggested line survives
// being pasted back into a shell.
func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = shellArg(a)
	}
	return out
}

// nearestFlag returns the flag cmd actually has whose name is within a typo's
// distance of the rejected one ("--dry-runn" → "--dry-run"), or "" when the
// flag is simply one rig doesn't own. Shorthands are skipped: a single letter
// is inside edit distance of most other letters, so every mistyped shorthand
// would draw a confident wrong guess.
//
// The budget scales with length for the same reason. Two edits over a short
// name is not a typo, it's a different word — at distance 2, "--nope" reaches
// "--open" and "--port" reaches "--root", and a wrong guess is worse than
// none, since it sends the reader off to check a flag they never meant.
func nearestFlag(cmd *cobra.Command, name string) string {
	typed := strings.TrimPrefix(name, "--")
	if typed == name || len(typed) < 3 { // a shorthand, or too short to guess from
		return ""
	}
	budget := 1
	if len(typed) >= 6 {
		budget = 2
	}
	best, bestDist := "", budget+1
	consider := func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		if d := editDistance(typed, f.Name); d < bestDist {
			best, bestDist = "--"+f.Name, d
		}
	}
	// All three sets: a command's own flags, the persistent ones it declares
	// (not folded into Flags() until it runs), and those it inherits.
	cmd.Flags().VisitAll(consider)
	cmd.PersistentFlags().VisitAll(consider)
	cmd.InheritedFlags().VisitAll(consider)
	return best
}

// editDistance is the Levenshtein distance between a and b, used only to judge
// whether a rejected flag is a misspelling of a real one.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
