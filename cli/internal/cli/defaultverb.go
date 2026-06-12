package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// newDefaultCmd builds `rig default [project]` — show or set `defaultProject`
// without running anything, the port of the .NET rig's DefaultVerb. With a
// name it validates against the runnable projects and persists; with no name
// it prompts interactively (or prints the current value in a non-interactive
// shell). Writes go through the comment-preserving config writer.
func newDefaultCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "default [project]",
		Short:             "Show or set the default project for run/publish",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: runnableProjectCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := detect.Root(cwd)
			var query string
			if len(args) == 1 {
				query = strings.TrimSpace(args[0])
			}
			return runDefault(cmd, root, query)
		},
	}
}

// runDefault is the verb body, split from the cobra wiring so tests can drive
// it against a temp repo root.
func runDefault(cmd *cobra.Command, root, query string) error {
	cfg, _ := config.LoadMerged(root)
	projects := detect.DiscoverDotNet(root, cfg.Solution, cfg.Exclude)
	var runnable []detect.ProjectInfo
	for _, p := range projects {
		if p.IsRunnable() {
			runnable = append(runnable, p)
		}
	}

	if query == "" {
		return defaultNoArg(cmd, root, cfg, runnable)
	}

	// `defaultProject` is deliberately ignored for the resolution: the query
	// names the NEW default, the old one must not steer it.
	res := resolveRunProject(projects, query, "")
	if res.Err != "" {
		return fmt.Errorf("%s%s", res.Err, runnableList(runnable))
	}
	target := res.Selected
	if target == nil { // ambiguous
		if !interactive() {
			return fmt.Errorf("%q matches multiple projects:%s", query, runnableList(res.Ambiguous))
		}
		picked, err := pickProject("Which project?", res.Ambiguous)
		if err != nil {
			return errSilent
		}
		target = picked
	}
	return persistDefault(cmd, root, target.Name)
}

// defaultNoArg handles a bare `rig default`: print the current value when the
// shell isn't interactive, otherwise prompt to set one (the .NET DefaultVerb's
// NoArg path).
func defaultNoArg(cmd *cobra.Command, root string, cfg config.Config, runnable []detect.ProjectInfo) error {
	out := cmd.OutOrStdout()
	if !interactive() {
		if cfg.DefaultProject == "" {
			fmt.Fprintln(out, "No default project set.")
		} else {
			fmt.Fprintf(out, "defaultProject = %s\n", cfg.DefaultProject)
		}
		return nil
	}
	if len(runnable) == 0 {
		return fmt.Errorf("no runnable projects found")
	}
	picked, err := pickProject("Set default project", runnable)
	if err != nil {
		return errSilent
	}
	return persistDefault(cmd, root, picked.Name)
}

// pickProject shows an interactive picker over the projects and returns the
// choice. Caller must have confirmed a TTY.
func pickProject(title string, projects []detect.ProjectInfo) (*detect.ProjectInfo, error) {
	var chosen int
	opts := make([]huh.Option[int], 0, len(projects))
	for i, p := range projects {
		opts = append(opts, huh.NewOption(p.Name, i))
	}
	sel := huh.NewSelect[int]().Title(title).Options(opts...).Value(&chosen)
	if err := sel.Run(); err != nil {
		return nil, err
	}
	return &projects[chosen], nil
}

// persistDefault writes defaultProject = name to the repo's .rig.json via the
// comment-preserving writer and reports where it landed.
func persistDefault(cmd *cobra.Command, root, name string) error {
	path := config.SetRepoString(root, "defaultProject", name)
	fmt.Fprintf(cmd.OutOrStdout(), "Set defaultProject = %s in %s\n", name, filepath.Base(path))
	return nil
}

// runnableList renders project names as an indented bullet list for error
// detail (the .NET DefaultVerb prints the same list after its error line).
func runnableList(projects []detect.ProjectInfo) string {
	var b strings.Builder
	for _, p := range projects {
		b.WriteString("\n  • " + p.Name)
	}
	return b.String()
}
