package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/core/config"
	"github.com/spf13/cobra"
)

// changesetReadme is the README for a changeset-files workspace. renderReadme
// branches on the chosen source for the commit-driven variants.
const changesetReadme = `# Changesets

This folder holds changesets — intent files describing pending releases. Add one
with ` + "`changerig add`" + `; consume them with ` + "`changerig version`" + `.
`

// NewInitCmd builds the `init` command.
func NewInitCmd() *cobra.Command {
	var sourceFlag string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the .changeset folder and config",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := Open()
			if err != nil {
				return err
			}
			source, err := resolveInitSource(sourceFlag, relDir(ws.Root, ws.ChangesetDir))
			if err != nil {
				return err
			}
			created, err := Scaffold(ws, source)
			if err != nil {
				return err
			}
			if !created {
				fmt.Fprintf(cmd.OutOrStdout(), "Already initialized at %s\n", ws.ChangesetDir)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Initialized changesets in %s (source: %s)\n", ws.ChangesetDir, source)
			return nil
		},
	}
	cmd.Flags().StringVar(&sourceFlag, "source", "",
		"release source: changesets (default), commits, or both")
	return cmd
}

// resolveInitSource picks the versioning source for a fresh workspace. An
// explicit --source wins (and is validated); otherwise, on a TTY, the user is
// asked via the shared source picker; off a TTY it defaults to changesets so a
// scripted `init` stays non-interactive.
func resolveInitSource(flag, where string) (config.VersioningSource, error) {
	if flag != "" {
		s, ok := config.ParseSource(flag)
		if !ok {
			return "", fmt.Errorf("invalid --source %q (want changesets|commits|both)", flag)
		}
		return s, nil
	}
	if addInteractive() {
		if s, ok := pickSource(where); ok {
			return s, nil
		}
		return "", fmt.Errorf("setup cancelled")
	}
	return config.SourceChangesets, nil
}

// Scaffold writes the .changeset folder, config, and README for ws under the
// given versioning source. It reports whether it created the config (false means
// the workspace was already initialized — a benign no-op). Shared by `init` and
// the inline setup offer the commands make in an uninitialized workspace.
func Scaffold(ws *Workspace, source config.VersioningSource) (created bool, err error) {
	if err := os.MkdirAll(ws.ChangesetDir, 0o755); err != nil {
		return false, err
	}
	cfgPath := filepath.Join(ws.ChangesetDir, "config.json")
	if _, err := os.Stat(cfgPath); err == nil {
		return false, nil
	}
	if err := os.WriteFile(cfgPath, []byte(renderConfig(source)), 0o644); err != nil {
		return false, err
	}
	readmePath := filepath.Join(ws.ChangesetDir, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		_ = os.WriteFile(readmePath, []byte(renderReadme(source)), 0o644)
	}
	return true, nil
}

// renderConfig produces the default config.json. Changeset mode omits the
// versioning block entirely (an empty source normalizes to changesets), so the
// classic config round-trips byte-for-byte; commit/both modes inject the source.
func renderConfig(source config.VersioningSource) string {
	versioning := ""
	if source == config.SourceCommits || source == config.SourceBoth {
		versioning = fmt.Sprintf("  \"versioning\": { \"source\": %q },\n", source)
	}
	return fmt.Sprintf(`{
  "$schema": "https://rigsmith.dev/schemas/changeset-config.json",
%s  "baseBranch": "main",
  "access": "restricted",
  "updateInternalDependencies": "patch",
  "ignore": [],
  "linked": [],
  "fixed": []
}
`, versioning)
}

// renderReadme returns the .changeset/README.md text for the chosen source,
// telling the user how releases are actually driven in this repo.
func renderReadme(source config.VersioningSource) string {
	switch source {
	case config.SourceCommits:
		return `# Changesets

This repo derives releases from **conventional commits** (versioning.source =
"commits"). Write commits like ` + "`feat(pkg): …`" + ` or ` + "`fix(pkg): …`" + ` and
` + "`changerig version`" + ` turns them into version bumps and changelogs — no
changeset files needed.
`
	case config.SourceBoth:
		return `# Changesets

This repo unions **conventional commits** with on-disk changeset files
(versioning.source = "both"). Most releases come from commit messages; add an
explicit changeset with ` + "`changerig add`" + ` when a commit can't capture the
intent. Consume both with ` + "`changerig version`" + `.
`
	default:
		return changesetReadme
	}
}
