package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/auth"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/envstack"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/rigsmith/rigsmith/internal/shiprig/pipeline"
	"github.com/spf13/cobra"
)

// newInitCmd builds `shiprig init`. It does everything `changerig init` does
// (scaffold .changeset/ and its config) and then layers the release side on top:
// a starter release.jsonc, any build-config file an ecosystem needs (Go ->
// .goreleaser.yaml), and a token preflight. The release layer is driven entirely
// by each ecosystem's ReleaseInit declaration — shiprig init holds no
// per-ecosystem knowledge of its own.
func newInitCmd() *cobra.Command {
	cmd := commands.NewInitCmd()
	cmd.Short = "Set up changesets and the release pipeline (config, build tooling, token preflight)"
	cmd.Long = "init scaffolds the changeset workspace (like changerig init), then sets up the\n" +
		"release side: a starter release pipeline config, any build-config file an\n" +
		"ecosystem needs to produce artifacts, and a preflight of the tokens a real\n" +
		"release will require. It writes files and checks the environment — it never\n" +
		"collects secrets or publishes anything."
	base := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		if err := base(c, args); err != nil {
			return err
		}
		return releaseInitLayer(c)
	}
	return cmd
}

// releaseInitLayer adds the release-pipeline setup on top of the changeset
// scaffold: the release config, per-ecosystem build configs, and the token
// preflight. It is read/scaffold/report only.
func releaseInitLayer(cmd *cobra.Command) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	ws, err := commands.Open()
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "\nRelease setup:")

	// 1. Scaffold the release pipeline config (optional at runtime, but a written
	// starter is how the steps and gates become discoverable).
	cfgPath := filepath.Join(ws.ChangesetDir, "release.jsonc")
	if created, err := scaffoldReleaseConfig(cfgPath); err != nil {
		return err
	} else if created {
		fmt.Fprintf(out, "  wrote %s (release pipeline config)\n", relTo(ws.Root, cfgPath))
	} else {
		fmt.Fprintf(out, "  %s already present — leaving as is\n", relTo(ws.Root, cfgPath))
	}

	// 2. Ask each detected ecosystem what it needs to release.
	pkgs, ecoOf, err := ws.Discover(ctx)
	if err != nil {
		return err
	}
	byEco := map[string][]plugin.Package{}
	for _, p := range pkgs {
		id := ecoOf[p.Name]
		byEco[id] = append(byEco[id], p)
	}

	var tokens []plugin.TokenSpec
	var authRefs []authRefEntry
	seenToken := map[string]bool{}
	for _, id := range sortedKeys(byEco) {
		eco, ok := ws.EcosystemFor(id)
		if !ok || !hasCapability(eco.Info(), plugin.MethodReleaseInit) {
			continue
		}
		// OIDC is "in play" unless explicitly turned off (default/auto = on).
		oidcEnabled := !strings.EqualFold(ws.Config.EcoConfig(id).OIDC, "off")
		ri, err := eco.ReleaseInit(ctx, plugin.ReleaseInitRequest{RepoRoot: ws.Root, Packages: byEco[id], OIDC: oidcEnabled})
		if err != nil {
			fmt.Fprintf(out, "  %s: release-init failed: %v\n", eco.Info().DisplayName, err)
			continue
		}
		name := eco.Info().DisplayName
		if ref := ws.Config.EcoConfig(id).Auth; ref != "" {
			authRefs = append(authRefs, authRefEntry{eco: name, ref: ref})
		}
		for _, n := range ri.Notes {
			fmt.Fprintf(out, "  %s: %s\n", name, n)
		}
		if ri.BuildConfig != nil {
			if err := handleBuildConfig(out, ws.Root, name, ri.BuildConfig); err != nil {
				return err
			}
		}
		for _, t := range ri.Tokens {
			if !seenToken[t.EnvVar] {
				seenToken[t.EnvVar] = true
				tokens = append(tokens, t)
			}
		}
	}

	// 3. Preflight the tokens a real release will need (reported, never stored).
	// Check against the same layered .env/.env.local < ambient view the release
	// runs with (honouring --no-env), so a token in a local .env reads as set.
	env, err := loadReleaseEnv(ws.Root, noEnv)
	if err != nil {
		return err
	}
	printTokenPreflight(out, tokens, env)
	printAuthPreflight(ctx, out, authRefs)
	return nil
}

// authRefEntry pairs an ecosystem with its configured publish-auth secret ref.
type authRefEntry struct {
	eco string
	ref string
}

// printAuthPreflight reports readiness of each configured publish-auth ref
// (op://, env:, cmd:) without resolving the secret — so the wizard surfaces a
// missing `op` CLI or a signed-out 1Password session before a release run hits
// it. Nothing here reads or stores a secret value.
func printAuthPreflight(ctx context.Context, out io.Writer, refs []authRefEntry) {
	if len(refs) == 0 {
		return
	}
	fmt.Fprintln(out, "\nPublish auth (configured refs, never stored):")
	for _, r := range refs {
		detail, ok := auth.PreflightRef(ctx, r.ref)
		mark := "⚠"
		if ok {
			mark = "✓"
		}
		fmt.Fprintf(out, "  %s %s  %s — %s\n", mark, r.eco, r.ref, detail)
	}
}

// scaffoldReleaseConfig writes a starter release.jsonc when none exists,
// returning whether it created the file. An existing config is left untouched.
func scaffoldReleaseConfig(path string) (created bool, err error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.WriteFile(path, []byte(releaseConfigStarter()), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// releaseConfigStarter renders a commented release.jsonc whose step order is the
// engine's own DefaultOrder, so the file can never drift from the real default.
func releaseConfigStarter() string {
	quoted := make([]string, len(pipeline.DefaultOrder))
	for i, s := range pipeline.DefaultOrder {
		quoted[i] = `"` + s + `"`
	}
	return `{
  // shiprig release pipeline — generated by ` + "`shiprig init`" + `.
  // Every key is optional: with no file, the defaults below still apply.
  // Preview a run with ` + "`shiprig release --dry-run`" + `; build only with ` + "`--dry-build`" + `.

  // Base tool for the built-in version/publish/tag steps.
  "tool": "shiprig",

  // The steps shiprig runs, in order (this is the built-in default — reorder or
  // drop as needed). version → commit → build → publish → tag → push → release.
  "order": [` + strings.Join(quoted, ", ") + `],

  "steps": {
    // Pause for confirmation before anything leaves the machine.
    "publish": { "confirm": true },
    "push": { "confirm": true },
    // Forge for the GitHub release: "auto" detects from origin, "none" = tags only.
    "release": { "forge": "auto" }
  }
}
`
}

// handleBuildConfig scaffolds an ecosystem's build-config file. An existing file
// is reported and left alone; an absent one is offered (on a TTY) or written by
// default, then the build tool's presence is preflighted.
func handleBuildConfig(out io.Writer, root, ecoName string, bc *plugin.BuildConfigSpec) error {
	if bc.Present {
		fmt.Fprintf(out, "  %s: %s already present — leaving as is\n", ecoName, bc.Path)
		return nil
	}
	if commands.Interactive() && !confirmGenerate(bc.Path, bc.Tool) {
		hint := bc.Tool
		if hint == "" {
			hint = "the build tool"
		}
		fmt.Fprintf(out, "  %s: skipped %s — configure %s yourself when ready\n", ecoName, bc.Path, hint)
		return nil
	}
	if err := os.WriteFile(filepath.Join(root, bc.Path), []byte(bc.Content), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "  %s: wrote %s\n", ecoName, bc.Path)
	if bc.Tool != "" {
		if _, err := exec.LookPath(bc.Tool); err != nil {
			fmt.Fprintf(out, "    ⚠ %s not on PATH (needed to build) — %s\n", bc.Tool, toolHint(bc.Tool))
		}
	}
	return nil
}

// confirmGenerate asks whether to write a build-config file. Aborting declines.
func confirmGenerate(path, tool string) bool {
	yes := true
	desc := "A starter config, templated from what shiprig found. You can edit it after."
	if tool != "" {
		desc = "A starter " + tool + " config, templated from what shiprig found. You can edit it after."
	}
	err := huh.NewConfirm().
		Title("Generate " + path + "?").
		Description(desc).
		Affirmative("Yes").
		Negative("No").
		Value(&yes).
		WithTheme(brand.Theme(brand.AccentShip)).
		Run()
	if err != nil {
		return false
	}
	return yes
}

// printTokenPreflight reports which release tokens are set, never reading their
// values. Presence is checked against env, the layered release environment
// (.env/.env.local < ambient), so a token in a local .env counts as set — the
// same view the release will run with. A nil env falls back to the process
// environment. Missing ones list what they're for and where to get one.
func printTokenPreflight(out io.Writer, tokens []plugin.TokenSpec, env map[string]string) {
	if len(tokens) == 0 {
		return
	}
	tokenSet := func(name string) bool {
		if env != nil {
			v, _ := envstack.Lookup(env, name) // case-insensitive on Windows
			return v != ""
		}
		return os.Getenv(name) != ""
	}
	fmt.Fprintln(out, "\nRelease tokens (checked, never stored):")
	for _, t := range tokens {
		if tokenSet(t.EnvVar) {
			fmt.Fprintf(out, "  ✓ %s  (%s)\n", t.EnvVar, t.For)
			continue
		}
		line := fmt.Sprintf("  ⚠ %s not set  (%s)", t.EnvVar, t.For)
		if t.URL != "" {
			line += " — " + t.URL
		}
		fmt.Fprintln(out, line)
	}
}

// hasCapability reports whether an ecosystem advertises the given method.
func hasCapability(info plugin.EcosystemInfo, method string) bool {
	for _, c := range info.Capabilities {
		if c == method {
			return true
		}
	}
	return false
}

// toolHint points at where to get a build tool that isn't on PATH.
func toolHint(tool string) string {
	switch tool {
	case "goreleaser":
		return "https://goreleaser.com/install/"
	default:
		return "install " + tool + " to build artifacts"
	}
}

// relTo renders p relative to root for display, falling back to p.
func relTo(root, p string) string {
	if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

// sortedKeys returns the map keys in sorted order, for stable output.
func sortedKeys(m map[string][]plugin.Package) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
