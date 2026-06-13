package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// defaultRigJSON is the scaffold written by `rig init`. Plain JSON (rig's loader
// reads JSON, not JSONC), with every optional key shown.
const defaultRigJSON = `{
  "$schema": "https://rigsmith.dev/schemas/rig.json",
  "defaultProject": "",
  "ecosystem": "",
  "quiet": false,
  "exclude": [],
  "env": {},
  "commands": {},
  "kill": { "match": [] }
}
`

// runInitWizard asks a few questions (seeded from what's detected) and returns
// the .rig.json content to write. ok=false means the user cancelled (esc).
func runInitWizard(cmd *cobra.Command, root string) (content string, ok bool, err error) {
	ctx := cdContext(cmd)

	// Offer the ecosystems actually present, defaulting to the nearest one.
	ecoOptions := []huh.Option[string]{huh.NewOption("Auto-detect (don't pin)", "")}
	for _, eco := range detectedEcosystems(ctx, root) {
		ecoOptions = append(ecoOptions, huh.NewOption(ecoDisplayName(eco), eco))
	}
	eco := ""
	if id, candidates := detect.NearestEcosystem(root); id != "" {
		eco = id
	} else if len(candidates) > 0 {
		eco = candidates[0]
	}

	// Default project only makes sense when there are several runnable .NET
	// projects to choose between.
	runnable := runnableDotnetNames(root)
	defaultProject := ""

	quiet := false

	fields := []huh.Field{
		huh.NewSelect[string]().
			Title("Primary ecosystem").
			Description("What rig should assume when several ecosystems coexist.").
			Options(ecoOptions...).
			Value(&eco),
	}
	if len(runnable) > 1 {
		dpOptions := []huh.Option[string]{huh.NewOption("(none)", "")}
		for _, n := range runnable {
			dpOptions = append(dpOptions, huh.NewOption(n, n))
		}
		fields = append(fields, huh.NewSelect[string]().
			Title("Default project").
			Description("The project `rig run`/`default` targets when none is named.").
			Options(dpOptions...).
			Value(&defaultProject))
	}
	fields = append(fields, huh.NewConfirm().
		Title("Quiet by default?").
		Description("Suppress the `→ command` echo on every run.").
		Value(&quiet))

	form := huh.NewForm(huh.NewGroup(fields...)).WithKeyMap(huhEscKeyMap())
	if err := form.Run(); err != nil {
		return "", false, nil // esc / ctrl+c → cancelled
	}
	return wizardRigJSON(eco, defaultProject, quiet), true, nil
}

// detectedEcosystems returns the distinct ecosystems present in the workspace,
// in display order, for the init wizard's ecosystem picker.
func detectedEcosystems(ctx context.Context, root string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range discoverWorkspace(ctx, root, excludeFor(root)) {
		if !seen[t.Eco] {
			seen[t.Eco] = true
			out = append(out, t.Eco)
		}
	}
	sort.Strings(out)
	return out
}

// runnableDotnetNames lists the repo's runnable .NET project short names (the
// default-project candidates), sorted. Empty for non-.NET repos.
func runnableDotnetNames(root string) []string {
	cfg, _ := config.LoadMerged(root)
	var names []string
	for _, p := range detect.DiscoverDotNet(root, cfg.Solution, cfg.Exclude) {
		if p.IsRunnable() {
			names = append(names, p.ShortName())
		}
	}
	sort.Strings(names)
	return names
}

// wizardRigJSON renders the scaffold with the wizard's chosen values filled in.
func wizardRigJSON(eco, defaultProject string, quiet bool) string {
	return fmt.Sprintf(`{
  "$schema": "https://rigsmith.dev/schemas/rig.json",
  "defaultProject": %q,
  "ecosystem": %q,
  "quiet": %t,
  "exclude": [],
  "env": {},
  "commands": {},
  "kill": { "match": [] }
}
`, defaultProject, eco, quiet)
}

// setDefaultProject validates query against the repo's runnable projects and
// persists `defaultProject` in the repo's .rig.json via the comment-preserving
// config writer, returning the config path written. It writes nothing when the
// query matches no runnable project (error) or several (ambiguous — the caller
// decides). Port of the .NET rig's DefaultVerb set path; the full verb
// (interactive picker, current-value display) lives in defaultverb.go.
func setDefaultProject(root string, cfg config.Config, query string) (string, error) {
	projects := detect.DiscoverDotNet(root, cfg.Solution, cfg.Exclude)
	res := resolveRunProject(projects, query, "")
	if res.Err != "" {
		return "", errors.New(res.Err)
	}
	if res.Selected == nil {
		return "", fmt.Errorf("%q matches multiple projects", query)
	}
	return config.SetRepoString(root, "defaultProject", res.Selected.Name), nil
}

// newRigInitCmd scaffolds a .rig.json at the repo root. It refuses to overwrite.
// On an interactive terminal it runs a short wizard (ecosystem / default project
// / quiet) seeded from what's detected; `--yes` (or a non-TTY) writes the plain
// scaffold instead.
func newRigInitCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold a .rig.json",
		Long: "Scaffold a .rig.json at the repo root.\n\n" +
			"On an interactive terminal it runs a short wizard (primary ecosystem, " +
			"default project, quiet) with detected defaults; pass --yes to skip it " +
			"and write the plain scaffold.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			path := filepath.Join(root, config.FileName)
			if _, err := os.Stat(path); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "%s already exists at %s\n", config.FileName, root)
				return nil
			}

			content := defaultRigJSON
			if !yes && interactive() {
				c, ok, err := runInitWizard(cmd, root)
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render("init cancelled"))
					return nil
				}
				content = c
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the wizard and write the plain scaffold")
	return cmd
}
