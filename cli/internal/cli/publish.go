package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// newPublishCmd builds `rig publish [project]` — the generic self-contained
// `dotnet publish`, ported from the .NET rig's PublishVerb. RID /
// self-contained / single-file / output come from the .rig.json `publish`
// block with sane defaults; a CLI flag always wins over config (the same
// precedence as coverage). .NET-only for now: other ecosystems have no single
// "publish" analogue.
func newPublishCmd() *cobra.Command {
	var (
		rid           string
		output        string
		configuration string
		selfContained bool
		singleFile    bool
	)

	cmd := &cobra.Command{
		Use:   "publish [project]",
		Short: "Publish a self-contained build (.NET)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := detect.Root(cwd)
			cfg, _ := config.Load(root)

			// A solution or project at the root is .NET regardless of what the
			// generic resolver says (it keys off per-ecosystem manifests, and a
			// solution-only root has none).
			if !hasDotNet(root) {
				eco, err := resolvePrimary(cwd, root)
				if err != nil {
					return err
				}
				if eco != detect.DotNet {
					return fmt.Errorf("publish is only supported for .NET repos (ecosystem here is %q)", eco)
				}
			}

			var query string
			if len(args) == 1 {
				query = strings.TrimSpace(args[0])
			}
			projects := detect.DiscoverDotNet(root, cfg.Solution, cfg.Exclude)
			res := resolveRunProject(projects, query, cfg.DefaultProject)
			if res.Err != "" {
				return fmt.Errorf("%s", res.Err)
			}
			if res.Selected == nil {
				names := make([]string, len(res.Ambiguous))
				for i, p := range res.Ambiguous {
					names[i] = p.Name
				}
				return fmt.Errorf("ambiguous project (%s) — pass a project name to `rig publish`",
					strings.Join(names, ", "))
			}

			// Precedence (matching the .NET rig): CLI flag > config > built-in default.
			pub := cfg.Publish
			effRid := rid
			if strings.TrimSpace(effRid) == "" {
				effRid = resolvePublishRid(pub)
			}
			effConfig := configuration
			if strings.TrimSpace(effConfig) == "" {
				if pub != nil && strings.TrimSpace(pub.Configuration) != "" {
					effConfig = pub.Configuration
				} else {
					effConfig = "Release"
				}
			}
			effSelfContained := true
			if cmd.Flags().Changed("self-contained") {
				effSelfContained = selfContained
			} else if pub != nil && pub.SelfContained != nil {
				effSelfContained = *pub.SelfContained
			}
			effSingleFile := false
			if cmd.Flags().Changed("single-file") {
				effSingleFile = singleFile
			} else if pub != nil && pub.SingleFile != nil {
				effSingleFile = *pub.SingleFile
			}
			rel := output
			if strings.TrimSpace(rel) != "" {
				rel = strings.ReplaceAll(rel, "{rid}", effRid)
			} else {
				rel = resolvePublishOutput(pub, effRid)
			}
			outputDir := filepath.Join(root, rel)

			argv := append([]string{"dotnet"},
				buildPublishArgs(res.Selected.FullPath, effConfig, effRid, effSelfContained, effSingleFile, outputDir)...)
			if err := runCommand(cmd, root, argv); err != nil {
				return err
			}
			if !dryRun {
				fmt.Fprintln(cmd.OutOrStdout(), okStyle.Render("Published: "+outputDir))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&rid, "rid", "", "runtime identifier (default: the host's)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "output directory template ({rid} expands; default dist/{rid})")
	cmd.Flags().StringVarP(&configuration, "configuration", "c", "", "build configuration (default Release)")
	cmd.Flags().BoolVar(&selfContained, "self-contained", true, "bundle the runtime")
	cmd.Flags().BoolVar(&singleFile, "single-file", false, "produce a single-file executable")
	return cmd
}
