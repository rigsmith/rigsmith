package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/confkit"
	"github.com/spf13/cobra"
)

// newStackAddCmd adds a repo to the manifest and imports it, so nobody has to
// hand-edit jsonc to grow a stackspace.
func newStackAddCmd() *cobra.Command {
	var fork, as string
	var owned, skipImport bool
	cmd := &cobra.Command{
		Use:   "add [upstream]",
		Short: "Add a repo to this stackspace and import it",
		Long: "Adds a repo to rig.stack.jsonc and imports it, without hand-editing the\n" +
			"manifest. Give the repo as host/owner/name or paste its URL — the https\n" +
			"one from your browser, the one the clone button offers, or an ssh remote.\n\n" +
			"With no argument it asks. Every answer is also a flag, so the same thing\n" +
			"scripts:\n\n" +
			"  rig stack add github.com/acme/pty-core --fork github.com/you/pty-core\n" +
			"  rig stack add github.com/you/term-app --owned --as app\n\n" +
			"`--owned` marks a repo as yours rather than a fork you contribute to, which\n" +
			"is what makes `rig stack push` available for it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			m, src, _, err := stackspace(ctx)
			if err != nil {
				return err
			}

			upstream := ""
			if len(args) == 1 {
				upstream = args[0]
			}
			interactive := upstream == ""
			if interactive {
				if err := huh.NewInput().
					Title("Upstream repo").
					Description("host/owner/name, or paste its URL").
					Value(&upstream).Run(); err != nil {
					return err
				}
			}
			upstream = stackNormalizeSpec(upstream)
			if upstream == "" {
				return fmt.Errorf("no repo given")
			}

			if as == "" {
				as = stackDefaultPrefix(upstream)
				if interactive {
					if err := huh.NewInput().
						Title("Directory it lives under").
						Description("also the name you pass to the verbs").
						Value(&as).Run(); err != nil {
						return err
					}
				}
			}
			if interactive && !owned {
				if err := huh.NewConfirm().
					Title("Is this repository yours?").
					Description("yours: `push` fast-forwards it, history intact\nsomeone else's: `send` proposes a squashed branch to your fork").
					Value(&owned).Run(); err != nil {
					return err
				}
			}
			// A repo of your own has no separate fork to propose changes to: the
			// place work goes is the place it came from.
			if owned && fork == "" {
				fork = upstream
			}
			if fork == "" {
				if !interactive {
					return fmt.Errorf("no fork given — pass --fork, or --owned when the repo is yours")
				}
				if err := huh.NewInput().
					Title("Your fork").
					Description("where `rig stack send` pushes PR-ready branches; you need push access").
					Value(&fork).Run(); err != nil {
					return err
				}
			}
			fork = stackNormalizeSpec(fork)

			if m.Repos[as] != nil {
				return fmt.Errorf("%s is already in this stackspace (%s) — pick another directory with --as", as, m.Repos[as].Upstream)
			}
			entry := &stackRepo{Upstream: upstream, Fork: fork, Owned: owned}
			probe := &stackManifest{Repos: map[string]*stackRepo{as: entry}}
			if err := probe.validate(); err != nil {
				return err
			}

			// Indented to sit at the depth the splice lands on: this file is read
			// and hand-edited, and an entry on one long line is the difference
			// between a manifest and a blob.
			raw, err := json.MarshalIndent(entry, "    ", "  ")
			if err != nil {
				return err
			}
			path := []string{"repos", as}
			if src.Path == "" { // embedded stack block in .rig.json
				path = []string{"stack", "repos", as}
			}
			w := confkit.Writer{SchemaURL: stackSchemaURL}
			if !w.Set(src.File, path, string(raw)) {
				return fmt.Errorf("could not add %s to %s — add it by hand", as, src.File)
			}
			fmt.Fprintf(out, "✓ added %s → %s\n", as, upstream)

			if skipImport {
				fmt.Fprintln(out, "  run `rig stack init` to import it")
				return nil
			}
			// Importing needs the manifest committed: an import amends its merge
			// commit and stages everything, so an uncommitted edit would be
			// swallowed into it. init says so itself when the tree is dirty.
			sub := newStackInitCmd()
			sub.SetContext(ctx)
			sub.SetOut(out)
			sub.SetErr(cmd.ErrOrStderr())
			return sub.RunE(sub, nil)
		},
	}
	cmd.Flags().StringVar(&fork, "fork", "", "your fork, where `send` pushes branches")
	cmd.Flags().StringVar(&as, "as", "", "directory to fuse it under (default: the repo name)")
	cmd.Flags().BoolVar(&owned, "owned", false, "this repo is yours, so `push` applies rather than `send`")
	cmd.Flags().BoolVar(&skipImport, "no-import", false, "write the manifest entry without importing")
	return cmd
}

// stackDefaultPrefix is the directory a repo lands under when nobody says
// otherwise: its own name, which is always a legal directory and is what
// someone typing the verbs will reach for.
func stackDefaultPrefix(spec string) string {
	parts := strings.Split(spec, "/")
	return parts[len(parts)-1]
}
