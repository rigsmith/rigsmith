package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/gitutil"
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
	var sourceFlag, changelogFlag string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the .changeset folder and config",
		Long: `Create the .changeset folder and its config.json.

--source picks where releases come from: changeset files (the default),
conventional commits, or both. On a GitHub repository, --changelog github
writes @changesets/changelog-github, so each changelog entry links its commit
and pull request and thanks its author; --changelog default keeps the plain
layout. Without the flag, init asks on a terminal, and otherwise keeps the
plain layout and says how to switch.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := Open()
			if err != nil {
				return err
			}
			source, err := resolveInitSource(sourceFlag, relDir(ws.Root, ws.ChangesetDir))
			if err != nil {
				return err
			}
			slug := gitutil.GitHubRepoSlug(cmd.Context(), ws.Root)
			repo, err := resolveInitChangelog(changelogFlag, slug)
			if err != nil {
				return err
			}
			created, err := ScaffoldWith(ws, source, repo)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !created {
				fmt.Fprintf(out, "Already initialized at %s\n", ws.ChangesetDir)
				return nil
			}
			fmt.Fprintf(out, "Initialized changesets in %s (source: %s)\n", ws.ChangesetDir, source)
			if repo == "" && slug != "" && changelogFlag == "" {
				// The config exists now, so init won't rewrite it: the edit
				// is the way to switch.
				fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf(
					"tip: this repository is on GitHub (%s). To link each changelog entry's commit and pull request, set %s in %s.",
					slug, changelogGitHubEntry(slug), filepath.Join(relDir(ws.Root, ws.ChangesetDir), "config.json"))))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sourceFlag, "source", "",
		"release source: changesets (default), commits, or both")
	cmd.Flags().StringVar(&changelogFlag, "changelog", "",
		"changelog layout: github (link commits and pull requests; needs a GitHub remote) or default")
	_ = cmd.RegisterFlagCompletionFunc("changelog", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"github\tlink commits and pull requests (@changesets/changelog-github)", "default\tthe plain layout"}, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// resolveInitChangelog decides whether a fresh config links commits and pull
// requests (@changesets/changelog-github), returning the GitHub repo to link
// to, or "" for the plain layout. An explicit --changelog wins; otherwise, on
// a GitHub repository at a terminal, the user is asked; anywhere else the
// plain layout stays, as @changesets' own init writes it.
func resolveInitChangelog(flag, slug string) (string, error) {
	switch flag {
	case "github":
		if slug == "" {
			return "", fmt.Errorf("--changelog github needs a GitHub remote to link to; this repository has none")
		}
		return slug, nil
	case "default":
		return "", nil
	case "":
	default:
		return "", fmt.Errorf("invalid --changelog %q (want github or default)", flag)
	}
	if slug == "" || !addInteractive() {
		return "", nil
	}
	link := true
	err := huh.NewConfirm().
		Title("Link commits and pull requests in the changelog?").
		Description(fmt.Sprintf("This repository is on GitHub (%s). @changesets/changelog-github links each entry's commit and pull request and thanks its author.", slug)).
		Value(&link).
		WithTheme(brand.Theme(brand.AccentChange)).
		Run()
	if err != nil {
		return "", fmt.Errorf("setup cancelled") // as the source picker: abort writes nothing
	}
	if !link {
		return "", nil // declined: the plain layout
	}
	return slug, nil
}

// changelogGitHubEntry is the `changelog` config entry that links repo's
// commits and pull requests, JSON-encoded whatever the repo string holds.
func changelogGitHubEntry(repo string) string {
	name, _ := json.Marshal(repo)
	return fmt.Sprintf(`"changelog": ["@changesets/changelog-github", { "repo": %s }]`, name)
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
	return ScaffoldWith(ws, source, "")
}

// ScaffoldWith is Scaffold that, given a GitHub repo ("owner/name"), writes
// @changesets/changelog-github linked to it, for commit and PR links.
func ScaffoldWith(ws *Workspace, source config.VersioningSource, githubRepo string) (created bool, err error) {
	if err := os.MkdirAll(ws.ChangesetDir, 0o755); err != nil {
		return false, err
	}
	cfgPath := filepath.Join(ws.ChangesetDir, "config.json")
	if _, err := os.Stat(cfgPath); err == nil {
		return false, nil
	}
	if err := os.WriteFile(cfgPath, []byte(renderConfig(source, githubRepo)), 0o644); err != nil {
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
func renderConfig(source config.VersioningSource, githubRepo string) string {
	versioning := ""
	if source == config.SourceCommits || source == config.SourceBoth {
		versioning = fmt.Sprintf("  \"versioning\": { \"source\": %q },\n", source)
	}
	if githubRepo != "" {
		versioning += "  " + changelogGitHubEntry(githubRepo) + ",\n"
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
