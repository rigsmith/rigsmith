package commands

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/editor"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/since"
	"github.com/spf13/cobra"
)

// NewAddCmd builds the `add` command (also the default command for both binaries).
func NewAddCmd() *cobra.Command {
	var (
		message  string
		bumpStr  string
		typeStr  string
		scopeStr string
		packages []string
		empty    bool
		sinceRef string
		open     bool
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a changeset describing pending releases",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := Open()
			if err != nil {
				return err
			}
			if !ws.Initialized() {
				ready, err := offerSetup(cmd, ws)
				if err != nil {
					return err
				}
				if !ready {
					return nil // declined the setup offer — nothing to create
				}
			}

			// In pure commit mode the commits themselves are the release source,
			// so there is nothing to add — guide the user to conventional commits
			// instead. ("both" mode still accepts file changesets alongside.)
			if ws.Config.CommitSource() == config.SourceCommits {
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render(
					"versioning.source is \"commits\" — releases come from conventional commits, not changeset files."))
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render(
					"Write a conventional commit (e.g. `feat(pkg): …`) instead. Set versioning.source to \"both\" to also use changeset files."))
				return nil
			}

			pkgs, _, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}
			// allNames validates explicit --package values; names is the releasable
			// subset the picker offers and we suggest on a typo. Ignored packages
			// stay nameable (version deliberately keeps changesets that name only
			// ignored packages) — they're just not offered or suggested.
			allNames := make([]string, 0, len(pkgs))
			names := make([]string, 0, len(pkgs))
			for _, p := range pkgs {
				allNames = append(allNames, p.Name)
				if !ws.Config.IsIgnored(p.Name) {
					names = append(names, p.Name)
				}
			}
			sort.Strings(allNames)
			sort.Strings(names)

			// Validate any --package names against the workspace up front. Without
			// this a typo (or a wrong short name like "rigsmith" for the module
			// "github.com/rigsmith/rigsmith") is written verbatim, and only
			// `version` later rejects the changeset as naming an unknown package.
			if len(packages) > 0 {
				known := make(map[string]bool, len(allNames))
				for _, n := range allNames {
					known[n] = true
				}
				var unknown []string
				for _, p := range packages {
					if !known[p] {
						unknown = append(unknown, p)
					}
				}
				if len(unknown) > 0 {
					return fmt.Errorf("unknown package(s): %s\nworkspace packages: %s",
						strings.Join(unknown, ", "), strings.Join(names, ", "))
				}
			}

			selected := packages
			bump := bumpStr
			typ := strings.TrimSpace(typeStr)
			summary := message

			// --since preselects the packages owning files changed since the
			// ref in the interactive picker (it does not skip the prompts).
			var preselect []string
			if sinceRef != "" {
				changedFiles, err := gitutil.ChangedFilesSince(cmd.Context(), ws.Root, sinceRef)
				if err != nil {
					return fmt.Errorf("could not determine changes since %q: %w", sinceRef, err)
				}
				preselect = since.ChangedProjectNames(changedFiles, pkgs, ws.Root)
			}

			// The scope decides which tool a changelog bullet is filed under, and
			// it is the easiest thing to forget — so work it out from the files
			// the branch touched rather than relying on anyone remembering. An
			// explicit "-" is how you say it belongs to no one tool.
			scope := strings.TrimSpace(scopeStr)
			switch scope {
			case "-":
				scope = ""
			case "":
				scope = inferScope(cmd.Context(), ws.Root, ws.Config.BaseBranch, sinceRef)
			}

			// Interactive only when nothing was given. With a --type (or --bump) and
			// --message + --package, we skip prompts entirely.
			if !empty && len(selected) == 0 && bump == "" && typ == "" && summary == "" {
				selected = preselect
				if err := runAddForm(names, &selected, &bump, &summary, &scope); err != nil {
					return err
				}
				scope = strings.TrimSpace(scope)
			}

			// A conventional prefix in the message fills in whatever the flags did
			// not say, and is then stripped: keeping it would leave two sources
			// disagreeing — notably `--scope -` against a `feat(rig):` summary,
			// where the prose would win when the changelog is rendered.
			if mt, ms, mbreak, ok := changeset.ParseConventionalScope(summary); ok {
				if typ == "" {
					typ = mt
					if mbreak {
						typ += "!"
					}
				}
				if scopeStr == "" && scope == "" {
					scope = ms
				}
				summary = changeset.StripConventional(summary)
			}

			// A breaking `!` suffix on the type sets breaking and the bump derives.
			breaking := strings.HasSuffix(typ, "!")
			typ = strings.TrimSuffix(typ, "!")

			// Bump resolution: an explicit --bump is the override; otherwise, when a
			// type is given the per-package bump is left as `auto` (BumpNone) and the
			// engine derives it from the type. With neither, default to patch.
			pkgBump := changeset.BumpNone
			switch {
			case bump != "":
				b, ok := changeset.ParseBump(bump)
				if !ok {
					return fmt.Errorf("invalid bump %q (want major|minor|patch|auto)", bump)
				}
				pkgBump = b
			case typ == "":
				pkgBump = changeset.BumpPatch
			}

			// A changeset that names no package is silently ignored by every
			// later step, so "Created …" would be a lie. With one package there
			// is nothing to choose; with several, say so rather than write a
			// file that does nothing.
			if !empty && len(selected) == 0 {
				switch len(names) {
				case 0:
					return fmt.Errorf("no packages found to write a changeset for")
				case 1:
					selected = []string{names[0]}
				default:
					return fmt.Errorf("name the packages this affects with -p (have: %s), or run `changerig add` with no flags to pick them",
						strings.Join(names, ", "))
				}
			}

			var releases []changeset.Release
			if !empty {
				for _, n := range selected {
					releases = append(releases, changeset.Release{Name: n, Bump: pkgBump})
				}
			}

			id := generateID()
			path := filepath.Join(ws.ChangesetDir, id+".md")
			content := changeset.RenderScoped(releases, summary, typ, scope, breaking)
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", filepath.Join(".changeset", id+".md"))

			if open {
				if err := openInEditor(cmd, path); err != nil {
					// The changeset is written; opening it is a convenience.
					fmt.Fprintf(cmd.ErrOrStderr(), "could not open editor: %v\n", err)
				}
			}

			// Auto-commit the new changeset when the `commit` config key is enabled
			// (mirrors @changesets). Only the changeset file is staged.
			if ws.Config.CommitEnabled() {
				msg := strings.TrimSpace(strings.SplitN(summary, "\n", 2)[0])
				if msg == "" {
					msg = "Add changeset"
				}
				if _, err := gitutil.CommitPaths(cmd.Context(), ws.Root, msg, path); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "could not commit changeset: %v\n", err)
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&message, "message", "m", "", "changeset summary (skip the prompt)")
	f.StringVar(&bumpStr, "bump", "", "explicit bump override (major|minor|patch|auto)")
	f.StringVarP(&typeStr, "type", "t", "", "conventional type (feat|fix|…, suffix ! for breaking); bump derives from it")
	f.StringVar(&scopeStr, "scope", "", "which tool the change belongs to; inferred from the changed files when omitted (\"-\" for none)")
	// Completion offers the scopes this repo actually uses — the tools under
	// cmd/ — so the value is discoverable rather than guessed at.
	_ = cmd.RegisterFlagCompletionFunc("scope", func(c *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		ws, err := Open()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return knownScopes(ws.Root), cobra.ShellCompDirectiveNoFileComp
	})
	f.StringSliceVarP(&packages, "package", "p", nil, "package to include (repeatable)")
	f.BoolVar(&empty, "empty", false, "write an empty changeset (names no packages)")
	f.StringVar(&sinceRef, "since", "", "preselect the packages changed since this git ref in the picker")
	f.BoolVar(&open, "open", false, "open the created changeset in $EDITOR")
	return cmd
}

// openInEditor opens path in the resolved editor (see core/editor), inheriting
// the terminal so the edit is interactive.
func openInEditor(cmd *cobra.Command, path string) error {
	argv := editor.Argv(path)
	c := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
	return c.Run()
}

// offerSetup handles a bare command in a workspace with no .changeset folder. On
// an interactive terminal it shows where config will live and runs the source
// picker (changeset files / conventional commits / both) inline, so setup is one
// prompt away rather than a dead-end error, then scaffolds the chosen source and
// reloads ws.Config so the caller sees it. It reports whether the workspace is
// ready to proceed (scaffolded or already set up). Off a TTY — CI, pipes — it
// can't ask, so it returns a clear, actionable error pointing at `<tool> init`,
// wrapping ErrNotSetUp so a bare invocation can report it as orientation rather
// than a failure (see BareRunE).
// Shared by `add` (before creating a changeset) and `status` (before planning).
func offerSetup(cmd *cobra.Command, ws *Workspace) (ready bool, err error) {
	tool := cmd.Root().Name()
	where := relDir(ws.Root, ws.ChangesetDir)
	if !addInteractive() {
		return false, fmt.Errorf("%w — run `%s init` to create %s (use --source commits for conventional-commit releases)", ErrNotSetUp, tool, where)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("Not set up in %s yet.", ws.Root)))
	source, ok := pickSource(where)
	if !ok {
		fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("Nothing set up. Run `%s init` when you're ready.", tool)))
		return false, nil
	}

	created, err := Scaffold(ws, source)
	if err != nil {
		return false, err
	}
	if created {
		fmt.Fprintf(out, "%s %s\n", PatchStyle.Render("✓"), fmt.Sprintf("Initialized %s (source: %s)", where, source))
	}
	// Reload config from disk so the caller routes on the chosen source rather
	// than the changesets default Open() loaded before the workspace existed.
	if c, e := config.Load(ws.ChangesetDir); e == nil {
		ws.Config = c
	}
	return true, nil
}

// pickSource runs the release-source chooser used by both the inline setup offer
// and interactive `init`. It returns the chosen source and whether the user
// confirmed; an aborted prompt (esc/ctrl+c) reports ok=false. Callers must only
// invoke it on a TTY (see addInteractive).
func pickSource(where string) (source config.VersioningSource, ok bool) {
	source = config.SourceChangesets
	err := huh.NewSelect[config.VersioningSource]().
		Title("How do you want to drive releases?").
		Description(fmt.Sprintf("Writes %sconfig.json.", where)).
		Options(
			huh.NewOption("Changeset files — explicit intent files (changerig add)", config.SourceChangesets),
			huh.NewOption("Conventional commits — releases derive from commit messages", config.SourceCommits),
			huh.NewOption("Both — changeset files and conventional commits", config.SourceBoth),
		).
		Value(&source).
		WithTheme(brand.Theme(brand.AccentChange)).
		Run()
	if err != nil {
		return config.SourceChangesets, false // aborted — treat as a decline
	}
	return source, true
}

// addInteractive reports whether `add` can prompt — both stdin and stdout must
// be a real terminal (matches browseInteractive).
func addInteractive() bool { return Interactive() }

// Interactive reports whether both stdin and stdout are real terminals. It is
// the shared gate for any surface that would block on input — the inline setup
// offer, and (in changerig/shiprig main) the bare-invocation menu.
func Interactive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// relDir renders dir relative to root for display, falling back to the absolute
// path. A trailing separator keeps directory paths legible (".changeset/").
func relDir(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return dir
	}
	return rel + string(filepath.Separator)
}

// knownScopes lists the scopes a repo can meaningfully use: the tools it builds
// and the packages it builds them from — the same two roots inferScope reads,
// so completion cannot offer less than inference will produce.
func knownScopes(root string) []string {
	seen := map[string]bool{}
	for _, dir := range []string{"cmd", "internal"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				seen[e.Name()] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// inferScope names the tool a change belongs to by looking at what the branch
// changed: a path under cmd/<tool> or internal/<tool> names that tool. Returns
// "" when the change spans several, or none — which is the honest answer, and
// files the bullet under the section rather than under a tool it only half
// belongs to.
func inferScope(ctx context.Context, root, base, sinceRef string) string {
	ref := sinceRef
	if ref == "" {
		ref = defaultScopeRef(ctx, root, base)
	}
	if ref == "" {
		return ""
	}
	files, err := gitutil.ChangedFilesSince(ctx, root, ref)
	if err != nil {
		return ""
	}
	found := map[string]bool{}
	for _, f := range files {
		// ChangedFilesSince returns paths joined to the repository root, so the
		// first component is the filesystem root rather than cmd/ or internal/.
		rel, err := filepath.Rel(root, f)
		if err != nil {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 2 {
			continue
		}
		if parts[0] == "cmd" || parts[0] == "internal" {
			found[parts[1]] = true
		}
	}
	if len(found) != 1 {
		return "" // spans several tools, or none of them
	}
	for name := range found {
		return name
	}
	return ""
}

// defaultScopeRef is the base branch to diff against when --since was not
// given. On the mainline itself there is no branch to compare, so inference
// stays quiet rather than diffing main against itself.
func defaultScopeRef(ctx context.Context, root, base string) string {
	repo, err := gitrepo.Open(ctx, root)
	if err != nil {
		return ""
	}
	// The configured baseBranch is the repo saying which branch it releases
	// from; without one, gitrepo already knows how to find the default (it
	// reads origin/HEAD before falling back to main/master/trunk), and
	// guessing "main" here would silently infer nothing on a master repo.
	if base == "" {
		base = repo.DefaultBranch(ctx)
	}
	cur, err := repo.CurrentBranch(ctx)
	if err != nil || base == "" || cur == base {
		return ""
	}
	return base
}

func runAddForm(names []string, selected *[]string, bump, summary, scope *string) error {
	if *bump == "" {
		*bump = "patch"
	}
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Which packages are affected?").
				Options(huh.NewOptions(names...)...).
				Value(selected),
		),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Bump type for these packages").
				Options(
					huh.NewOption("patch — bug fixes", "patch"),
					huh.NewOption("minor — new features", "minor"),
					huh.NewOption("major — breaking changes", "major"),
				).
				Value(bump),
			huh.NewText().
				Title("Summary").
				Placeholder("Describe the change for the changelog").
				Value(summary),
			// Pre-filled from what the branch touched. Shown rather than applied
			// silently, because the inference is a guess about intent and the
			// person typing the summary is the one who knows.
			huh.NewInput().
				Title("Scope").
				Description("which tool this belongs to in the changelog; blank for none").
				Value(scope),
		),
	).WithTheme(brand.Theme(brand.AccentChange))
	return form.Run()
}

var (
	adjectives = []string{"brave", "calm", "clever", "eager", "fuzzy", "gentle", "happy", "jolly", "kind", "lucky", "mighty", "nimble", "proud", "quiet", "swift", "witty"}
	animals    = []string{"otters", "pandas", "falcons", "lions", "geckos", "dolphins", "badgers", "herons", "foxes", "ravens", "wombats", "lemurs", "moose", "yaks", "ibex", "shrimp"}
	verbs      = []string{"dance", "dream", "glow", "jump", "march", "ponder", "race", "sing", "wander", "whisper", "build", "sparkle"}
)

// generateID returns a human-friendly changeset filename stem.
func generateID() string {
	return strings.Join([]string{
		adjectives[rand.Intn(len(adjectives))],
		animals[rand.Intn(len(animals))],
		verbs[rand.Intn(len(verbs))],
	}, "-")
}
