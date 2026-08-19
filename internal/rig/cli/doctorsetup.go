package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// setupGroup is the doctor group id for the rows about rig's own installation.
const setupGroup = "setup"

// setupChecks are doctor's rows about rig itself: which binary is answering and
// where it lives, which of the family is alongside it, and what rig has
// registered in the shell.
//
// They run everywhere, including a directory with no project — that early
// return used to skip the whole report, which is exactly the directory you'd be
// standing in when asking why `rcd` does nothing.
func setupChecks() []pendingCheck {
	state := readShellState()
	return []pendingCheck{
		{eco: setupGroup, label: "rig", run: rigBinaryCheck},
		{eco: setupGroup, label: "family", run: familyCheck},
		{eco: setupGroup, label: "shell", run: func() check { return shellIntegrationCheck(state) }},
		{eco: setupGroup, label: "aliases", run: func() check { return aliasCheck(state) }},
	}
}

// rigBinaryCheck reports the running rig — its version and the path it was
// executed from — and warns when PATH holds more than one, since the copy your
// shell finds first is the one that answers `rig`, and it is invisible until it
// answers something you meant for this one.
//
// Running a binary that isn't on PATH is not a fault: that is what a source
// build or a `rig-dev` launcher is. It says so, and names what typing `rig`
// would run instead, rather than crying wolf on a normal dev loop.
func rigBinaryCheck() check {
	label := versionLabel()
	exe := runningBinary()
	copies := pathCopies(integrationBase)

	if exe == "" {
		return ok("rig", label)
	}
	if len(copies) > 1 {
		which := "the first is " + copies[0]
		if copies[0] == exe {
			which = "the first is this one"
		}
		return warn("rig", fmt.Sprintf("%s · %d copies on your PATH (%s) — %s, so that is what `rig` runs",
			label, len(copies), strings.Join(copies, ", "), which))
	}
	switch {
	case len(copies) == 0:
		return ok("rig", label+" · "+exe+" (not on your PATH)")
	case copies[0] != exe:
		return ok("rig", label+" · running "+exe+"; typing `rig` runs "+copies[0])
	default:
		return ok("rig", label+" · "+exe)
	}
}

// versionLabel names this build the way `rig --version` does: a release
// version, or "source build" for one compiled locally.
func versionLabel() string {
	if version == "" || version == "dev" {
		return "source build"
	}
	return version
}

// familyCheck reports which sibling rigs are installed. It never warns: the
// family is optional, and `rig setup`'s block loads each one's completion only
// when it's on PATH, so installing one later needs no re-run — just a new
// shell. The row exists to answer "is clauderig actually installed, and which
// one am I getting".
func familyCheck() check {
	var found, missing []string
	dirs := map[string]bool{}
	paths := map[string]string{}
	for _, tool := range companionTools {
		path, present := onPath(tool)
		if !present {
			missing = append(missing, tool)
			continue
		}
		found = append(found, tool)
		paths[tool] = path
		dirs[filepath.Dir(path)] = true
	}

	var where string
	switch {
	case len(found) == 0:
		return ok("family", "none installed — "+strings.Join(missing, ", ")+" aren't on your PATH")
	case len(dirs) == 1:
		// The normal install: one directory holds them all, so name it once.
		where = strings.Join(found, ", ") + " · " + filepath.Dir(paths[found[0]])
	default:
		// Spread across directories — which one came from where is the point.
		var each []string
		for _, tool := range found {
			each = append(each, tool+" ("+paths[tool]+")")
		}
		where = strings.Join(each, ", ")
	}
	if len(missing) > 0 {
		where += " · " + strings.Join(missing, ", ") + " not installed (their completions are skipped)"
	}
	return ok("family", where)
}

// shellIntegrationCheck reports the `rig setup` block: the wrapper function
// that lets `rig cd` change your directory, and the completions. Absent or
// stale is a warn — both features fail silently, which is why they get asked
// about — but a shell rig has no snippet for is a fact, not a fault.
func shellIntegrationCheck(state shellState) check {
	switch {
	case state.shell == "":
		return ok("shell", shellUnsupportedDetail())
	case state.err != nil:
		return warn("shell", fmt.Sprintf("couldn't read your %s startup file: %v", state.shell, state.err))
	case state.setup == "":
		if state.devBlockPresent() {
			return warn("shell", fmt.Sprintf(
				"only the %s-dev block is in %s — plain `rig cd` and completion aren't wired; run `rig setup %s`",
				integrationBase, state.rcPath, state.shell))
		}
		return warn("shell", fmt.Sprintf(
			"not installed in %s — `rig cd` can't change your directory and tab completion is off; run `rig setup %s`",
			state.rcPath, state.shell))
	case state.setup != setupSnippet(state.shell, integrationBase):
		// "differs", not "older": a byte difference proves neither age nor
		// authorship. The block may come from a NEWER rig, or carry a
		// deliberate edit — and telling an older binary to overwrite a newer
		// block would quietly downgrade the integration.
		return warn("shell", fmt.Sprintf(
			"%s has a block that differs from the one this rig writes — `rig setup %s` replaces it with this build's",
			state.rcPath, state.shell))
	default:
		return ok("shell", state.rcPath+" · cd wrapper + completion for "+
			strings.Join(append([]string{integrationBase}, companionTools...), ", "))
	}
}

// shellUnsupportedDetail explains why there was nothing to check, naming what
// $SHELL actually says so the answer isn't "no".
func shellUnsupportedDetail() string {
	sh := strings.TrimSpace(os.Getenv("SHELL"))
	if sh == "" {
		sh = "$SHELL isn't set"
	} else {
		sh = "$SHELL is " + sh
	}
	return sh + " — rig writes snippets for " + strings.Join(setupShells, ", ") + " only"
}

// aliasCheck reports which short aliases are live in the rc file. Never a warn:
// aliases are opt-in, so having none is a choice, not a defect. A partial
// install is named as such, since the usual way to find out that `rt` was never
// installed is to type it.
func aliasCheck(state shellState) check {
	switch {
	case state.shell == "":
		return ok("aliases", "not checked — "+shellUnsupportedDetail())
	case state.err != nil:
		return ok("aliases", "not checked — that startup file couldn't be read")
	case state.aliases == "":
		return ok("aliases", "none installed — `rig alias install` adds "+aliasNames())
	}
	installed := installedAliases(state.shell, state.aliases)
	if len(installed) == 0 {
		return ok("aliases", "a block is in "+state.rcPath+" but defines none of "+aliasNames()+
			" — re-run `rig alias install`")
	}
	detail := fmt.Sprintf("%s · %s", aliasNamesOf(installed), state.rcPath)
	if len(installed) < len(rigAliases) {
		detail = fmt.Sprintf("%d of %d installed: %s · %s",
			len(installed), len(rigAliases), aliasNamesOf(installed), state.rcPath)
	}
	return ok("aliases", detail)
}
