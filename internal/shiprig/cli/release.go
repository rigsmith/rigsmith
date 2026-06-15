package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/envstack"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/sign"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/rigsmith/rigsmith/internal/shiprig/forge"
	"github.com/rigsmith/rigsmith/internal/shiprig/pipeline"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newReleaseCmd builds the `release` command: a configurable step pipeline
// (.changeset/release.jsonc) around the built-in version/commit/build/publish/
// push/release steps, with hooks, captured variables, confirm gates, and
// secret masking. The `build` step produces each package's distributable
// artifacts (the ecosystem Artifacts method) into dist/ before publish; the
// release step attaches the Attach:true ones. Ported from net-changesets.
func newReleaseCmd() *cobra.Command {
	var (
		dryRun     bool
		dryBuild   bool
		only, skip []string
		from, to   string
		configPath string
		yes        bool
		gitOnly    bool
		ui, noUI   bool
	)
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Run the release pipeline (version → commit → build → publish → tag → push → release)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := commands.Open()
			if err != nil {
				return err
			}

			path := configPath
			if path == "" {
				path = filepath.Join(ws.ChangesetDir, "release.jsonc")
			}
			cfg, err := pipeline.LoadConfig(path)
			if err != nil {
				return err
			}
			// The Go binaries are their own changeset engine — default the
			// built-in version/publish steps to shiprig itself, not the Node CLI.
			if cfg.Tool == "" {
				cfg.Tool = "shiprig"
			}

			// Ecosystem targeting (steps with an "ecosystems" filter) needs to know
			// which ecosystems this release touches and which ids are valid. Only
			// pay for discovery when a step actually opts in; otherwise leave both
			// sets nil so filtering and validation are no-ops.
			var presentEcos, knownEcos []string
			if configUsesEcosystems(cfg) {
				knownEcos = registryEcosystemIDs(ws)
				_, ecoOf, derr := ws.Discover(cmd.Context())
				if derr != nil {
					return derr
				}
				presentEcos = distinctEcosystems(ecoOf)
			}

			steps, err := pipeline.Resolve(cfg, pipeline.ResolveOptions{
				Only: only, Skip: skip, From: from, To: to, DryBuild: dryBuild,
				Ecosystems: presentEcos, KnownEcosystems: knownEcos,
			})
			if err != nil {
				return err
			}

			if dryBuild {
				// A dry-build only builds — no registry/forge side effects. Drop the
				// global hooks and captured vars so it can't trigger them (e.g. an OTP
				// prompt), and require an enabled build step so it actually does work.
				cfg.Hooks = nil
				cfg.Vars = nil
				if !hasEnabledStep(steps, "build") {
					return fmt.Errorf("nothing to dry-build: no enabled 'build' step in the release order")
				}
			}

			outRedirected := !term.IsTerminal(int(os.Stdout.Fd()))
			inRedirected := !term.IsTerminal(int(os.Stdin.Fd()))
			mode := pipeline.ResolveUIMode(ui, noUI, yes, outRedirected, inRedirected)
			// On a real terminal, a signing-secret resolution failure degrades to an
			// unsigned build with a warning; in CI (redirected) it's a hard error, so
			// a release that asked to be signed never ships unsigned unnoticed.
			interactive := !outRedirected && !inRedirected

			masker := pipeline.NewSecretMasker()

			// The layered .env/.env.local < ambient environment (skipped by
			// --no-env) so release commands, captured variables, ${env.NAME}
			// interpolation, and forge releases all see tokens declared in a local
			// .env without exporting them.
			releaseEnv, err := loadReleaseEnv(ws.Root, noEnv)
			if err != nil {
				return err
			}
			runnerEnv := envstack.Environ(releaseEnv)

			// release native handler: per-package forge releases. Output is
			// routed through the active reporter (so the live dashboard captures it
			// instead of writing raw to the terminal).
			fsel := forge.Selection{Forge: stepForge(cfg), URL: stepForgeURL(cfg)}
			if gitOnly {
				fsel.Forge = "none"
			}
			// built is shared between the `build` and `release` handlers in a
			// single run: build produces dist/ and records each package's artifacts;
			// the forge step attaches the Attach:true ones to the release.
			built := map[string][]plugin.Artifact{}
			distDir := filepath.Join(ws.Root, "dist")

			// Shell strings run through the in-process portable shell by default
			// (cross-platform); "shell": "system" opts into the OS shell. argv
			// commands are unaffected either way. cfg.Shell was validated by
			// LoadConfig, so the error here cannot fire.
			shellMode, _ := pipeline.ShellMode(cfg.Shell)
			releaseRunner := pipeline.NewPortableRunner(runnerEnv)
			if shellMode == pipeline.ShellSystem {
				releaseRunner = pipeline.NewExecRunner(runnerEnv)
			}

			newPipeline := func(reporter pipeline.Reporter, prompter pipeline.Prompter) *pipeline.Pipeline {
				out := func(lines ...string) { reporter.CommandOutput(lines) }

				buildHandler := func() bool {
					pkgs, ecoOf, err := ws.Discover(cmd.Context())
					if err != nil {
						out("discover: " + err.Error())
						return false
					}
					for k := range built {
						delete(built, k) // fresh each run
					}
					for _, pkg := range pkgs {
						eco, ok := ws.Registry.Get(ecoOf[pkg.Name])
						if !ok {
							continue
						}
						// Resolve optional build-time signing secrets for this ecosystem
						// (off unless a `signing` block enables it); masked and passed to
						// the build so the tool self-signs (macOS CSC_*/APPLE_*). Windows
						// artifacts are signed later by the `sign` step.
						env, ok := resolveSigningEnv(cmd.Context(), ws.Config, ecoOf[pkg.Name], masker, interactive, "build "+pkg.Name, out)
						if !ok {
							return false
						}
						var signing *plugin.SigningCreds
						if len(env) > 0 {
							signing = &plugin.SigningCreds{Env: env}
						}
						resp, err := eco.Artifacts(cmd.Context(), plugin.ArtifactsRequest{
							RepoRoot: ws.Root, Package: pkg, OutputDir: distDir, Snapshot: dryBuild, Signing: signing,
						})
						if err != nil {
							out("build " + pkg.Name + ": " + err.Error())
							return false
						}
						if resp.Built {
							built[pkg.Name] = resp.Artifacts
						}
						if resp.Message != "" {
							out("build " + pkg.Name + ": " + resp.Message)
						}
					}
					return true
				}

				// signHandler is the post-build `sign` step: it signs each package's
				// built Windows artifacts (.exe/.msi) via the configured signer (Azure
				// Trusted Signing by default). A no-op unless an ecosystem's
				// `signing.windows` is set. Signed in place, so the later `release` step
				// attaches the signed files. In --dry-build it previews the signer
				// commands without contacting the signing service.
				signHandler := func() bool {
					_, ecoOf, err := ws.Discover(cmd.Context())
					if err != nil {
						out("discover: " + err.Error())
						return false
					}
					signedAny := false
					for name, arts := range built {
						sc := ws.Config.EcoConfig(ecoOf[name]).Signing
						if sc == nil || !sc.Enabled || sc.Windows == nil {
							continue
						}
						if len(sign.SignableWindows(arts)) == 0 {
							continue
						}
						env, ok := resolveSigningEnv(cmd.Context(), ws.Config, ecoOf[name], masker, interactive, "sign "+name, out)
						if !ok {
							return false
						}
						files, output, serr := sign.SignWindows(cmd.Context(), arts, sc.Windows, env, runnerEnv, dryBuild)
						for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
							if line != "" {
								out("sign " + name + ": " + line)
							}
						}
						if serr != nil {
							out("sign " + name + ": " + serr.Error())
							return false
						}
						if len(files) > 0 {
							signedAny = true
							out(fmt.Sprintf("sign %s: signed %d Windows artifact(s)", name, len(files)))
						}
					}
					if !signedAny {
						out("sign: no Windows signing configured (skipping)")
					}
					return true
				}

				releaseHandler := func() bool {
					pkgs, ecoOf, err := ws.Discover(cmd.Context())
					if err != nil {
						out("discover: " + err.Error())
						return false
					}
					ok, msg := forge.Run(pkgs, ecoOf, attachPaths(built), ws.Config, fsel, ws.Root, execForgeRunner(cmd, runnerEnv), out)
					if msg != "" {
						out(msg)
					}
					return ok
				}

				issuesHandler := func() bool {
					ic := ws.Config.Issues
					if !ic.Enabled {
						out("issues: disabled (set issues.enabled to comment on / close resolved issues)")
						return true
					}
					pkgs, ecoOf, err := ws.Discover(cmd.Context())
					if err != nil {
						out("discover: " + err.Error())
						return false
					}
					// Label the release (fills the comment's {{version}} and the
					// dedupe marker) with the tags actually released.
					tags := make([]string, 0, len(pkgs))
					for _, p := range pkgs {
						if ws.Config != nil && ws.Config.IsIgnored(p.Name) {
							continue
						}
						tags = append(tags, gitutil.PackageTag(ecoOf[p.Name], p.Dir, p.Name, p.Version))
					}
					sort.Strings(tags)
					messages, err := releasedCommitMessages(cmd, ws.Root)
					if err != nil {
						out("issues: " + err.Error())
						return false
					}
					ok, msg := forge.RunIssues(messages, fsel,
						forge.IssuesConfig{Comment: ic.Comment, Close: ic.Close},
						strings.Join(tags, ", "), ws.Root, execForgeRunner(cmd, runnerEnv), out)
					if msg != "" {
						out(msg)
					}
					return ok
				}

				relctx := &hostReleaseContext{
					discover:      func() ([]plugin.Package, map[string]string, error) { return ws.Discover(cmd.Context()) },
					repoRoot:      ws.Root,
					forgeSel:      fsel,
					forgeRun:      execForgeRunner(cmd, runnerEnv),
					issueMessages: func() ([]string, error) { return releasedCommitMessages(cmd, ws.Root) },
					urlCache:      map[string]string{},
				}
				if ws.Config != nil {
					relctx.isIgnored = ws.Config.IsIgnored
				}

				return pipeline.New(releaseRunner, reporter, masker, prompter, ws.Root,
					releaseEnv, map[string]pipeline.NativeHandler{"build": buildHandler, "sign": signHandler, "release": releaseHandler, "issues": issuesHandler},
					relctx)
			}

			fail := func() error {
				cmd.SilenceUsage = true
				cmd.SilenceErrors = true // the reporter already told the story
				return errors.New("release failed")
			}

			// Full TUI flow (interactive, rich, real run): the plan editor lets the
			// user toggle steps, then the live dashboard drives the run with inline
			// confirm gates. A dry-build only builds (nothing to gate), so it takes
			// the straight sequential path like --dry-run.
			if mode.Interactive && mode.Rich && !dryRun && !dryBuild {
				chosen, proceed := interactiveChooser{
					in: cmd.InOrStdin(), out: cmd.OutOrStdout(), masker: masker,
				}.Choose(steps)
				if !proceed {
					fmt.Fprintln(cmd.OutOrStdout(), "Release cancelled.")
					return nil
				}
				ok, err := runDashboard(chosen, cfg, cfg.Tool,
					cmd.InOrStdin(), cmd.OutOrStdout(), masker, newPipeline)
				if err != nil {
					return err
				}
				if !ok {
					return fail()
				}
				return nil
			}

			// Sequential path (CI, --yes, piped, --no-ui, or --dry-run).
			var reporter pipeline.Reporter
			if mode.Rich {
				reporter = newRichReporter(cmd.OutOrStdout(), masker, cfg.Tool)
			} else {
				reporter = pipeline.NewPlainReporter(cmd.OutOrStdout(), masker, cfg.Tool)
			}
			var prompter pipeline.Prompter
			if mode.Interactive {
				prompter = ttyPrompter{}
			} else {
				// Non-interactive: --yes approves gates; otherwise a gate safely
				// stops the release rather than guessing.
				prompter = pipeline.FixedPrompter{Answer: yes}
			}
			if !newPipeline(reporter, prompter).Run(steps, cfg, dryRun) {
				return fail()
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&dryRun, "dry-run", "n", false, "preview the interpolated plan; only commands marked \"dryRun\" execute")
	f.BoolVar(&dryBuild, "dry-build", false, "build the release's artifacts locally (snapshot) and publish nothing — runs only the build step")
	f.StringSliceVar(&only, "only", nil, "run only these steps (comma-separated)")
	f.StringSliceVar(&skip, "skip", nil, "skip these steps (comma-separated)")
	f.StringVar(&from, "from", "", "start at this step (resume point)")
	f.StringVar(&to, "to", "", "stop after this step")
	f.StringVar(&configPath, "config", "", "release config file (default .changeset/release.jsonc)")
	f.BoolVarP(&yes, "yes", "y", false, "approve all confirm gates (non-interactive)")
	f.BoolVar(&gitOnly, "git-only", false, "skip forge (GitHub) releases; tags only")
	f.BoolVar(&ui, "ui", false, "force the rich reporter even when piped")
	f.BoolVar(&noUI, "no-ui", false, "force the plain reporter")
	// --dry-build, --dry-run, and the step-selection flags are three distinct
	// modes that don't compose: dry-run is plan-only, dry-build forces a build-only
	// plan, and --only/--skip/--from/--to hand-pick steps. Keep them exclusive so a
	// combination can't produce a confusing or no-op run.
	for _, mutex := range []string{"dry-run", "only", "skip", "from", "to"} {
		cmd.MarkFlagsMutuallyExclusive("dry-build", mutex)
	}
	return cmd
}

// configUsesEcosystems reports whether any step opts into ecosystem targeting,
// so the release path only pays for discovery when it must.
func configUsesEcosystems(cfg *pipeline.Config) bool {
	for _, sc := range cfg.Steps {
		if sc != nil && len(sc.Ecosystems) > 0 {
			return true
		}
	}
	return false
}

// registryEcosystemIDs lists the valid ecosystem ids, for validating a step's
// `ecosystems` against typos.
func registryEcosystemIDs(ws *commands.Workspace) []string {
	all := ws.Registry.All()
	ids := make([]string, 0, len(all))
	for _, eco := range all {
		ids = append(ids, eco.Info().ID)
	}
	return ids
}

// distinctEcosystems returns the sorted distinct ecosystem ids present in the
// release. The result is non-nil even when empty, so ecosystem filtering stays
// active (every targeted step is skipped) for a release that touches nothing.
func distinctEcosystems(ecoOf map[string]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ecoOf {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// hostReleaseContext implements pipeline.ReleaseContext from the workspace. It
// discovers the released packages once (lazily, on first variable reference) and
// exposes their versions, tags, and changelog notes; forge release URLs are
// fetched per package on demand (and cached); resolved issues come from the
// released commit messages.
//
// Versions/notes reflect the manifest and CHANGELOG at first reference, so a
// command needing the bumped value should run at/after the `version` step (the
// common case: publish args, the release step, and the after/onError hooks).
// ${releaseUrl} is only populated after the forge `release` step has run.
type hostReleaseContext struct {
	discover      func() ([]plugin.Package, map[string]string, error)
	isIgnored     func(string) bool
	repoRoot      string
	forgeSel      forge.Selection
	forgeRun      forge.Runner
	issueMessages func() ([]string, error)

	loaded bool
	pkgs   []pipeline.ReleasePackage

	urlCache map[string]string

	issuesLoaded bool
	issues       []pipeline.IssueRef
}

func (rc *hostReleaseContext) Packages() []pipeline.ReleasePackage {
	if rc.loaded {
		return rc.pkgs
	}
	rc.loaded = true

	pkgs, ecoOf, err := rc.discover()
	if err != nil {
		return nil // discovery errors surface via the handlers; variables resolve to empty
	}
	for _, p := range pkgs {
		if rc.isIgnored != nil && rc.isIgnored(p.Name) {
			continue
		}
		eco := ecoOf[p.Name]
		rc.pkgs = append(rc.pkgs, pipeline.ReleasePackage{
			Name:      p.Name,
			Key:       shortPackageKey(p.Name),
			Ecosystem: eco,
			Version:   p.Version,
			Tag:       gitutil.PackageTag(eco, p.Dir, p.Name, p.Version),
			Changelog: forge.Notes(p, rc.repoRoot),
		})
	}
	return rc.pkgs
}

// ReleaseURL fetches the forge release URL for the addressed package's tag once,
// caching the result. "" until the forge release step has created the release
// (or when the forge has no URL command).
func (rc *hostReleaseContext) ReleaseURL(key string) string {
	if rc.forgeRun == nil {
		return ""
	}
	if url, ok := rc.urlCache[key]; ok {
		return url
	}
	url := ""
	for _, p := range rc.Packages() {
		if p.Key == key || p.Name == key {
			url = forge.ReleaseURL(rc.forgeSel, p.Tag, rc.repoRoot, rc.forgeRun)
			break
		}
	}
	rc.urlCache[key] = url
	return url
}

// Issues lists the forge issues the released commits reference. Branch is left
// empty: shiprig has no issue-branch scheme in the release flow, so ${issues}
// resolves but ${issueBranch} stays empty.
func (rc *hostReleaseContext) Issues() []pipeline.IssueRef {
	if rc.issuesLoaded {
		return rc.issues
	}
	rc.issuesLoaded = true
	if rc.issueMessages == nil {
		return nil
	}
	messages, err := rc.issueMessages()
	if err != nil {
		return nil
	}
	for _, n := range forge.ResolvedIssueNumbers(messages) {
		rc.issues = append(rc.issues, pipeline.IssueRef{Number: n})
	}
	return rc.issues
}

// shortPackageKey is the ${version.<key>} address: the last path segment of the
// manifest name ("@acme/web" -> "web", "acme/cli" -> "cli"). The full name still
// works as an exact alias, so collisions remain addressable.
func shortPackageKey(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 && i+1 < len(name) {
		return name[i+1:]
	}
	return name
}

// hasEnabledStep reports whether the resolved plan contains an enabled step with
// the given name.
func hasEnabledStep(steps []pipeline.ResolvedStep, name string) bool {
	for _, s := range steps {
		if s.Name == name && s.Enabled() {
			return true
		}
	}
	return false
}

// stepForge reads the forge selection from the `release` step config.
func stepForge(cfg *pipeline.Config) string {
	if s, ok := cfg.Steps["release"]; ok && s != nil {
		return s.Forge
	}
	return ""
}

// stepForgeURL reads the self-hosted forge URL from the `release` step config.
func stepForgeURL(cfg *pipeline.Config) string {
	if s, ok := cfg.Steps["release"]; ok && s != nil {
		return s.ForgeURL
	}
	return ""
}

// releasedCommitMessages returns the subject+body of every commit in this
// release — the range since the previous release tag — for issue-ref scanning.
// The previous tag is the nearest one reachable from HEAD's parent (HEAD is the
// release commit, so HEAD^ is pre-release and its nearest tag is the prior
// release); when there is none (first release, shallow clone), the whole history
// is scanned.
func releasedCommitMessages(cmd *cobra.Command, root string) ([]string, error) {
	prev := ""
	if out, err := execForgeRunner(cmd, nil)(root, "git", "describe", "--tags", "--abbrev=0", "HEAD^"); err == nil {
		prev = strings.TrimSpace(out)
	}
	commits, err := gitutil.LogSince(cmd.Context(), root, prev)
	if err != nil {
		return nil, err
	}
	messages := make([]string, 0, len(commits))
	for _, c := range commits {
		messages = append(messages, c.Subject+"\n"+c.Body)
	}
	return messages, nil
}

// attachPaths flattens the build's artifacts into per-package file paths for the
// ones marked Attach (binaries/archives), dropping registry packages (.tgz/
// .nupkg/.crate) that ship to their registry rather than the forge release.
func attachPaths(built map[string][]plugin.Artifact) map[string][]string {
	out := map[string][]string{}
	for name, arts := range built {
		for _, a := range arts {
			if a.Attach {
				out[name] = append(out[name], a.Path)
			}
		}
	}
	return out
}

// resolveSigningEnv resolves an ecosystem's signing secret env, returning nil
// when signing is not configured/enabled. It applies the degrade policy on a
// resolution failure: interactive (a real terminal) warns and proceeds UNSIGNED
// (ok=true, nil env); non-interactive (CI) reports the error and fails the step
// (ok=false) — so a release that asked to be signed never ships unsigned
// unnoticed. label prefixes the warning/error (e.g. "build web" / "sign web").
func resolveSigningEnv(ctx context.Context, cfg *config.Config, eco string, masker *pipeline.SecretMasker, interactive bool, label string, out func(...string)) (map[string]string, bool) {
	sc := cfg.EcoConfig(eco).Signing
	if sc == nil || !sc.Enabled || len(sc.Env) == 0 {
		return nil, true
	}
	env, err := sign.ResolveEnv(ctx, sc.Env, masker)
	if err != nil {
		if interactive {
			out(label + ": signing secret unavailable — proceeding UNSIGNED (" + err.Error() + ")")
			return nil, true
		}
		out(label + ": " + err.Error())
		return nil, false
	}
	return env, true
}

// ttyPrompter asks a confirm gate on the terminal.
type ttyPrompter struct{}

func (ttyPrompter) Confirm(message string) bool {
	ok := false
	err := huh.NewConfirm().Title(message).Value(&ok).WithTheme(brand.Theme(brand.AccentShip)).Run()
	if err != nil {
		return false // treat an aborted prompt as a decline
	}
	return ok
}

// execForgeRunner adapts os/exec to the forge.Runner seam, running each command
// with env as its environment (the layered .env/.env.local < ambient stack; nil
// inherits the ambient process environment) so forge releases see the same
// tokens as the rest of the pipeline.
func execForgeRunner(cmd *cobra.Command, env []string) forge.Runner {
	return func(dir, name string, args ...string) (string, error) {
		c := exec.CommandContext(cmd.Context(), name, args...)
		c.Dir = dir
		c.Env = env // nil inherits the current process environment
		out, err := c.CombinedOutput()
		return string(out), err
	}
}
