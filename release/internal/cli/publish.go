package cli

import (
	"fmt"

	"github.com/rigsmith/changerig/commands"
	"github.com/rigsmith/core/gitutil"
	"github.com/rigsmith/core/plugin"
	"github.com/spf13/cobra"
)

// newPublishCmd publishes each discovered package to its ecosystem's registry
// (idempotently — already-published versions are skipped), then creates and
// pushes a git tag per package. Go modules have no registry push; they are
// published purely by the tag (module/vX.Y.Z), which the module proxy serves.
func newPublishCmd() *cobra.Command {
	var (
		dryRun   bool
		noGitTag bool
		noPush   bool
		access   string
	)
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish packages to their registries and tag the release",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := commands.Open()
			if err != nil {
				return err
			}
			pkgs, ecoOf, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			acc := access
			if acc == "" {
				acc = ws.Config.Access
			}

			// 1. Registry publish per package (ignored packages are never published).
			for _, p := range pkgs {
				if ws.Config.IsIgnored(p.Name) {
					continue
				}
				eco, ok := ws.EcosystemFor(ecoOf[p.Name])
				if !ok {
					continue
				}
				resp, err := eco.Publish(cmd.Context(), plugin.PublishRequest{
					RepoRoot:      ws.Root,
					Package:       p,
					PackageSource: ecosystemSource(ecoOf[p.Name]),
					Access:        acc,
					DryRun:        dryRun,
				})
				if err != nil {
					return fmt.Errorf("publish %s: %w", p.Name, err)
				}
				switch {
				case resp.Published:
					fmt.Fprintf(out, "%s %s@%s  %s\n", commands.PatchStyle.Render("published"), p.Name, p.Version, commands.DimStyle.Render(resp.Message))
				case resp.Skipped:
					fmt.Fprintf(out, "%s %s@%s  %s\n", commands.DimStyle.Render("skipped  "), p.Name, p.Version, commands.DimStyle.Render(resp.Message))
				default:
					fmt.Fprintf(out, "%s %s@%s  %s\n", commands.DimStyle.Render("·        "), p.Name, p.Version, commands.DimStyle.Render(resp.Message))
				}
			}

			// 2. Tagging phase (this is what actually publishes Go modules).
			if noGitTag {
				return nil
			}
			remote := ""
			if !noPush {
				remote = gitutil.DefaultRemote(cmd.Context(), ws.Root)
			}
			fmt.Fprintln(out)
			for _, p := range pkgs {
				if ws.Config.IsIgnored(p.Name) {
					continue
				}
				tag := tagName(ecoOf[p.Name], p.Dir, p.Name, p.Version)
				if gitutil.TagExists(cmd.Context(), ws.Root, tag) {
					fmt.Fprintf(out, "%s %s\n", commands.DimStyle.Render("tag exists"), tag)
					continue
				}
				if dryRun {
					push := ""
					if remote != "" {
						push = commands.DimStyle.Render(" → push " + remote)
					}
					fmt.Fprintf(out, "%s %s%s\n", commands.DimStyle.Render("would tag"), tag, push)
					continue
				}
				if err := gitutil.CreateTag(cmd.Context(), ws.Root, tag, tag); err != nil {
					return fmt.Errorf("tagging %s: %w", p.Name, err)
				}
				if remote != "" {
					if err := gitutil.PushTag(cmd.Context(), ws.Root, remote, tag); err != nil {
						return fmt.Errorf("pushing tag %s: %w", tag, err)
					}
					fmt.Fprintf(out, "%s %s %s\n", commands.PatchStyle.Render("tagged+pushed"), tag, commands.DimStyle.Render("→ "+remote))
				} else {
					fmt.Fprintf(out, "%s %s\n", commands.PatchStyle.Render("tagged"), tag)
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&dryRun, "dry-run", "n", false, "show what would be published/tagged without doing it")
	f.BoolVar(&noGitTag, "no-git-tag", false, "skip creating git tags")
	f.BoolVar(&noPush, "no-push", false, "create tags locally but do not push them")
	f.StringVar(&access, "access", "", "npm access (public|restricted); defaults to config")
	return cmd
}

// ecosystemSource returns the default package source per ecosystem when config
// doesn't specify one. The adapters fall back to their own defaults on "".
func ecosystemSource(eco string) string {
	switch eco {
	case "dotnet":
		return "nuget"
	default:
		return ""
	}
}
