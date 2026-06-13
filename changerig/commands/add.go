package commands

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"github.com/rigsmith/core/brand"
	"github.com/rigsmith/core/changeset"
	"github.com/rigsmith/core/gitutil"
	"github.com/rigsmith/core/since"
	"github.com/spf13/cobra"
)

// NewAddCmd builds the `add` command (also the default command for both binaries).
func NewAddCmd() *cobra.Command {
	var (
		message  string
		bumpStr  string
		typeStr  string
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
				ready, err := offerInit(cmd, ws)
				if err != nil {
					return err
				}
				if !ready {
					return nil // declined the setup offer — nothing to create
				}
			}

			pkgs, _, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}
			names := make([]string, 0, len(pkgs))
			for _, p := range pkgs {
				names = append(names, p.Name)
			}
			sort.Strings(names)

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

			// Interactive only when nothing was given. With a --type (or --bump) and
			// --message + --package, we skip prompts entirely.
			if !empty && len(selected) == 0 && bump == "" && typ == "" && summary == "" {
				selected = preselect
				if err := runAddForm(names, &selected, &bump, &summary); err != nil {
					return err
				}
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

			var releases []changeset.Release
			if !empty {
				for _, n := range selected {
					releases = append(releases, changeset.Release{Name: n, Bump: pkgBump})
				}
			}

			id := generateID()
			path := filepath.Join(ws.ChangesetDir, id+".md")
			content := changeset.Render(releases, summary, typ, breaking)
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
	f.StringSliceVarP(&packages, "package", "p", nil, "package to include (repeatable)")
	f.BoolVar(&empty, "empty", false, "write an empty changeset (names no packages)")
	f.StringVar(&sinceRef, "since", "", "preselect the packages changed since this git ref in the picker")
	f.BoolVar(&open, "open", false, "open the created changeset in $EDITOR")
	return cmd
}

// openInEditor opens path in the user's editor, inheriting the terminal so the
// edit is interactive.
func openInEditor(cmd *cobra.Command, path string) error {
	argv := append(resolveEditor(os.Getenv("VISUAL"), os.Getenv("EDITOR"), runtime.GOOS), path)
	c := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
	return c.Run()
}

// resolveEditor returns the editor command (program + any args) to launch:
// $VISUAL, then $EDITOR, else a per-OS default. Splitting on spaces honors
// forms like EDITOR="code -w". Pure.
func resolveEditor(visual, editorEnv, goos string) []string {
	editor := firstNonEmpty(visual, editorEnv)
	if editor == "" {
		if goos == "windows" {
			editor = "notepad"
		} else {
			editor = "vi"
		}
	}
	return strings.Fields(editor)
}

// firstNonEmpty returns the first argument that isn't blank, else "".
func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// offerInit handles a bare `add` in a workspace with no .changeset folder. On an
// interactive terminal it shows where changesets would live and offers to set
// them up inline (a huh confirm), so the first changeset is one prompt away
// rather than a dead-end error. It reports whether the workspace is ready to
// proceed (scaffolded or already set up). Off a TTY — CI, pipes — it can't ask,
// so it returns a clear, actionable error pointing at `changerig init`.
func offerInit(cmd *cobra.Command, ws *Workspace) (ready bool, err error) {
	where := relDir(ws.Root, ws.ChangesetDir)
	if !addInteractive() {
		return false, fmt.Errorf("changesets aren't set up here yet — run `changerig init` to create %s", where)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("No changesets set up in %s yet.", ws.Root)))
	setup := false
	if err := huh.NewConfirm().
		Title("Set up changesets here?").
		Description(fmt.Sprintf("Creates %s with a default config.", where)).
		Value(&setup).
		Run(); err != nil {
		return false, nil // aborted prompt (esc/ctrl+c) — treat as a decline, no error
	}
	if !setup {
		fmt.Fprintln(out, DimStyle.Render("No changeset created. Run `changerig init` when you're ready."))
		return false, nil
	}

	created, err := Scaffold(ws)
	if err != nil {
		return false, err
	}
	if created {
		fmt.Fprintf(out, "%s %s\n", PatchStyle.Render("✓"), fmt.Sprintf("Initialized changesets in %s", where))
	}
	return true, nil
}

// addInteractive reports whether `add` can prompt — both stdin and stdout must
// be a real terminal (matches browseInteractive).
func addInteractive() bool {
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

func runAddForm(names []string, selected *[]string, bump, summary *string) error {
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
