package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
)

// defaultRigJSON is the scaffold written by `rig init`. Plain JSON for the
// scaffold (the loader also accepts JSONC — comments/trailing commas), with
// every optional key shown.
const defaultRigJSON = `{
  "$schema": "https://rigsmith.dev/schemas/rig.json",
  "solution": "",
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

	// A one-line summary of what was found, so the choices below are informed.
	fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render("Detected: "+workspaceSummary(ctx, root)))

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

	solution := ""
	defaultProject := ""
	var excludeSel []string
	quiet := false

	fields := []huh.Field{
		huh.NewSelect[string]().
			Title("Primary ecosystem").
			Description("What rig should assume when several ecosystems coexist.").
			Options(ecoOptions...).
			Value(&eco),
	}

	// Solution: only when several .sln/.slnx files exist to choose between.
	if solutions := solutionFiles(root); len(solutions) > 1 {
		opts := []huh.Option[string]{huh.NewOption("(auto)", "")}
		for _, s := range solutions {
			opts = append(opts, huh.NewOption(s, s))
		}
		fields = append(fields, huh.NewSelect[string]().
			Title("Solution").
			Description("Which .sln rig builds/discovers against.").
			Options(opts...).
			Value(&solution))
	}

	// Default project: only when there are several runnable .NET projects.
	if runnable := runnableDotnetNames(root); len(runnable) > 1 {
		opts := []huh.Option[string]{huh.NewOption("(none)", "")}
		for _, n := range runnable {
			opts = append(opts, huh.NewOption(n, n))
		}
		fields = append(fields, huh.NewSelect[string]().
			Title("Default project").
			Description("The project `rig run`/`default` targets when none is named.").
			Options(opts...).
			Value(&defaultProject))
	}

	// Exclude: offer detected sample/example-ish dirs that hold packages.
	if candidates := excludeCandidates(ctx, root); len(candidates) > 0 {
		opts := make([]huh.Option[string], 0, len(candidates))
		for _, c := range candidates {
			opts = append(opts, huh.NewOption(c, c))
		}
		fields = append(fields, huh.NewMultiSelect[string]().
			Title("Exclude from discovery?").
			Description("Dirs to keep out of `--all`/`doctor` (space toggles).").
			Options(opts...).
			Value(&excludeSel))
	}

	fields = append(fields, huh.NewConfirm().
		Title("Quiet by default?").
		Description("Suppress the `→ command` echo on every run.").
		Value(&quiet))

	form := huh.NewForm(huh.NewGroup(fields...)).WithKeyMap(huhEscKeyMap()).WithTheme(rigTheme())
	if err := form.Run(); err != nil {
		return "", false, nil // esc / ctrl+c → cancelled
	}
	return wizardRigJSON(eco, solution, defaultProject, excludeSel, quiet), true, nil
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

// workspaceSummary is a one-line count of what discovery found, per ecosystem
// (e.g. "3 Go modules · 2 Node packages · 1 .NET project").
func workspaceSummary(ctx context.Context, root string) string {
	counts := map[string]int{}
	for _, t := range discoverWorkspace(ctx, root, excludeFor(root)) {
		counts[t.Eco]++
	}
	if len(counts) == 0 {
		return "no packages detected"
	}
	ecos := make([]string, 0, len(counts))
	for eco := range counts {
		ecos = append(ecos, eco)
	}
	sort.Strings(ecos)
	nouns := map[string]string{"go": "module", "node": "package", "dotnet": "project", "cargo": "crate"}
	parts := make([]string, 0, len(ecos))
	for _, eco := range ecos {
		noun := nouns[eco]
		if noun == "" {
			noun = "package"
		}
		n := counts[eco]
		if n != 1 {
			noun += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s %s", n, ecoDisplayName(eco), noun))
	}
	return strings.Join(parts, " · ")
}

// solutionFiles lists the .sln/.slnx files at the repo root (base names), sorted.
func solutionFiles(root string) []string {
	var out []string
	for _, pat := range []string{"*.sln", "*.slnx"} {
		hits, _ := filepath.Glob(filepath.Join(root, pat))
		for _, h := range hits {
			out = append(out, filepath.Base(h))
		}
	}
	sort.Strings(out)
	return out
}

// excludeDirNames are the top-level dir names commonly excluded from discovery.
var excludeDirNames = map[string]bool{
	"example": true, "examples": true, "sample": true, "samples": true,
	"fixture": true, "fixtures": true, "testdata": true, "demo": true,
	"demos": true, "e2e": true, "integration": true,
}

// excludeCandidates returns the top-level dirs (matching common sample/example
// names) that actually contain discovered packages — the exclude picker's
// options. Tying candidates to real packages avoids offering empty dirs.
func excludeCandidates(ctx context.Context, root string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range discoverWorkspace(ctx, root, excludeFor(root)) {
		rel, err := filepath.Rel(root, t.Dir)
		if err != nil {
			continue
		}
		top := rel
		if i := strings.IndexAny(rel, `/\`); i >= 0 {
			top = rel[:i]
		}
		if excludeDirNames[strings.ToLower(top)] && !seen[top] {
			seen[top] = true
			out = append(out, top)
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
func wizardRigJSON(eco, solution, defaultProject string, exclude []string, quiet bool) string {
	excludeJSON := "[]"
	if len(exclude) > 0 {
		quoted := make([]string, len(exclude))
		for i, e := range exclude {
			quoted[i] = fmt.Sprintf("%q", e)
		}
		excludeJSON = "[" + strings.Join(quoted, ", ") + "]"
	}
	return fmt.Sprintf(`{
  "$schema": "https://rigsmith.dev/schemas/rig.json",
  "solution": %q,
  "defaultProject": %q,
  "ecosystem": %q,
  "quiet": %t,
  "exclude": %s,
  "env": {},
  "commands": {},
  "kill": { "match": [] }
}
`, solution, defaultProject, eco, quiet, excludeJSON)
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
	path, ok := config.SetRepoString(root, "defaultProject", res.Selected.Name)
	if !ok {
		return "", fmt.Errorf("could not write defaultProject to %s: the existing file could not be edited in place", filepath.Base(path))
	}
	return path, nil
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
