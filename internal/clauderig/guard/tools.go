package guard

// The tools the guard acts on, in one place.
//
// This registry exists because the same list was previously written down twice —
// once as the PreToolUse matcher in the installed settings.json (which decides
// whether the hook runs at all), and once as Evaluate's switch (which decides
// what to do). Claude Code shipped `Monitor`, a tool that runs shell commands
// like Bash, and it was added to neither: the hook never fired for it, so every
// rule below was silently skipped. A third place gated the staged-file lookup,
// and missing it there left the base-branch commit protection off for Monitor
// even after the first two were fixed.
//
// So the lists are derived from here rather than repeated. hooks.GuardPlans
// builds the matcher from Tools(), and both Evaluate and the payload decoder ask
// the predicates. Adding a tool is one edit, in one file.

// commandTools execute a shell command. They can relocate the session with a
// `cd` and land a commit on a base branch, so they get the command rules.
var commandTools = []string{"Bash", "Monitor"}

// writeTools write to a file path, and get the base-branch write rules.
var writeTools = []string{"Edit", "Write", "NotebookEdit"}

// relocationTools move the session's working directory by their nature, which
// scrambles the chat history keyed to it. Always denied, repo or not.
var relocationTools = []string{"EnterWorktree", "ExitWorktree"}

// RunsCommand reports whether a tool executes a shell command.
func RunsCommand(tool string) bool { return in(commandTools, tool) }

// WritesFile reports whether a tool writes to a file path.
func WritesFile(tool string) bool { return in(writeTools, tool) }

// Relocates reports whether a tool moves the session's working directory.
func Relocates(tool string) bool { return in(relocationTools, tool) }

// Tools is every tool the guard acts on — the source the PreToolUse matcher is
// built from. A tool the guard handles but the matcher omits is not guarded at
// all, so these must not be allowed to drift apart.
func Tools() []string {
	out := make([]string, 0, len(writeTools)+len(commandTools)+len(relocationTools))
	out = append(out, writeTools...)
	out = append(out, commandTools...)
	return append(out, relocationTools...)
}

func in(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
