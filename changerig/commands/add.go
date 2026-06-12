package commands

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/core/changeset"
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
				return fmt.Errorf("not initialized — run `init` first")
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

			// Interactive only when nothing was given. With a --type (or --bump) and
			// --message + --package, we skip prompts entirely.
			if !empty && len(selected) == 0 && bump == "" && typ == "" && summary == "" {
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
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&message, "message", "m", "", "changeset summary (skip the prompt)")
	f.StringVar(&bumpStr, "bump", "", "explicit bump override (major|minor|patch|auto)")
	f.StringVarP(&typeStr, "type", "t", "", "conventional type (feat|fix|…, suffix ! for breaking); bump derives from it")
	f.StringSliceVarP(&packages, "package", "p", nil, "package to include (repeatable)")
	f.BoolVar(&empty, "empty", false, "write an empty changeset (names no packages)")
	return cmd
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
	)
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
