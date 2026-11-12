package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rigsmith/rigsmith/core/envstack"
	"github.com/rigsmith/rigsmith/core/secrets"
	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/spf13/cobra"
)

// noSecrets is --no-doppler: run this once without the secrets manager.
var noSecrets bool

// secretVerbs are the built-in verbs that build or run the project's own code,
// and so run under the secrets manager. The rest — installs, formatting,
// housekeeping — never need secrets, and must not demand a sign-in.
// Custom commands, package scripts and Go script verbs always qualify: rig
// cannot know what they do. They carry runsProjectCodeAnnotation.
var secretVerbs = map[string]bool{
	"build": true, "test": true, "run": true, "rebuild": true,
	"verify": true, "watch": true, "coverage": true, "publish": true,
}

// topVerb is the verb directly under the root that cmd belongs to (a menu-run
// verb has no parent, and is its own).
func topVerb(cmd *cobra.Command) *cobra.Command {
	for cmd.HasParent() && cmd.Parent().HasParent() {
		cmd = cmd.Parent()
	}
	return cmd
}

// runsProjectCodeAnnotation marks a surfaced script verb (custom command,
// package script, Go script verb) as running the repo's own code.
const runsProjectCodeAnnotation = "rigRunsProjectCode"

// secretsApply reports whether cmd's verb runs under the secrets manager.
//
// It must not ask isBuiltinVerbName: the first call builds a fresh root
// command, and registering its persistent flags resets dryRun, quiet and
// noSecrets mid-run — `rig -n test` would then really run.
func secretsApply(cmd *cobra.Command) bool {
	top := topVerb(cmd)
	return secretVerbs[top.Name()] || top.Annotations[runsProjectCodeAnnotation] != ""
}

// localSecrets is the resolved local setup for a run: the provider and the
// dev scope, or ok=false when this run reads no secrets — none configured,
// --no-doppler, a verb that needs none, CI (where the workflow passes each
// step its own secrets), or no dev config.
type localSecrets struct {
	provider secrets.Provider
	scope    secrets.Scope
}

// secretsFor resolves the local secrets setup for cmd. A broken provider block
// is an error: running without secrets because of a typo is exactly the silent
// failure this exists to prevent.
func secretsFor(cmd *cobra.Command) (localSecrets, bool, error) {
	if noSecrets || !secretsApply(cmd) || secrets.InCI() {
		return localSecrets{}, false, nil
	}
	cwd, _ := os.Getwd()
	// The strict loader, not the config rig reads for everything else: that
	// one degrades a broken file to defaults with a warning, which here would
	// mean running without secrets. shiprig decides with the same loader.
	setup, err := secrets.LoadLayered(resolveRoot(cwd), config.GlobalPath())
	if err != nil {
		return localSecrets{}, false, err
	}
	scope, ok := setup.Scope(secrets.Dev)
	if !ok {
		return localSecrets{}, false, nil
	}
	return localSecrets{provider: setup.Provider, scope: scope}, true, nil
}

// readyChecked memoises the readiness check per folder, so an --all run over
// twenty packages asks the CLI once.
var readyChecked = map[string]error{}

// ready checks the provider can serve this run, with rig's own way out
// appended to the provider's fix.
func (s localSecrets) ready(ctx context.Context, dir string) error {
	key := s.provider.Name() + "\x00" + dir
	err, seen := readyChecked[key]
	if !seen {
		err = s.provider.Ready(ctx, dir)
		readyChecked[key] = err
	}
	if err != nil {
		return fmt.Errorf("%w\n  (or run once without %s: --no-%s)", err, s.provider.Title(), s.provider.Name())
	}
	return nil
}

// withSecrets returns argv to run for cmd: wrapped in the provider
// (`doppler run -p P -c dev -- argv`) when this run reads secrets, unchanged
// otherwise. A dry run shows the wrapped line without asking the CLI anything.
func withSecrets(cmd *cobra.Command, dir string, argv []string) ([]string, error) {
	s, ok, err := secretsFor(cmd)
	if err != nil || !ok {
		return argv, err
	}
	if !dryRun {
		if err := s.ready(cmd.Context(), dir); err != nil {
			return nil, err
		}
	}
	return s.provider.Wrap(s.scope, argv), nil
}

// inProcessSecrets serves the command forms rig runs itself — a portable-shell
// line, a Tengo script — which have no single child to wrap. It reads the dev
// config's secrets once and returns them as the top environment layer, with
// the label the echo carries ("doppler foxcraft/dev"). A dry run reads
// nothing and returns the label alone.
func inProcessSecrets(cmd *cobra.Command, dir string) (map[string]string, string, error) {
	s, ok, err := secretsFor(cmd)
	if err != nil || !ok {
		return nil, "", err
	}
	label := s.provider.Name() + " " + s.scope.String()
	if dryRun {
		return nil, label, nil
	}
	if err := s.ready(cmd.Context(), dir); err != nil {
		return nil, "", err
	}
	all, err := s.provider.All(cmd.Context(), dir, s.scope)
	if err != nil {
		return nil, "", err
	}
	return all, label, nil
}

// overlayEnv lays vars over env (nil env is the process's own) and returns
// the result as an environ slice; secrets win, as `doppler run` gives them.
func overlayEnv(env []string, vars map[string]string) []string {
	if len(vars) == 0 {
		return env
	}
	if env == nil {
		env = os.Environ()
	}
	merged := make(map[string]string, len(env)+len(vars))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			merged[k] = v
		}
	}
	for k, v := range vars {
		merged[k] = v
	}
	return envstack.Environ(merged)
}

// secretsLabel prefixes an in-process command's echo with where its secrets
// came from, so the → line says the run is not a bare one.
func secretsLabel(label, line string) string {
	if label == "" {
		return line
	}
	return "[" + label + "] " + line
}

func newSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Show the secrets manager this repo reads, and write its CI fetch",
		Long: "secrets reports the secrets manager .rig.json configures (\"doppler\": {project, dev,\n" +
			"ci, release}), the config each context reads, and whether the CLI is ready.\n\n" +
			"Locally, build/test/run/rebuild/verify/watch/coverage/publish and every custom\n" +
			"command run under `doppler run -p <project> -c <dev>`; --no-doppler skips it once.\n" +
			"In CI rig wraps nothing: the workflow passes each step the secrets it needs.\n\n" +
			"  rig secrets               where secrets come from, and whether the CLI is ready\n" +
			"  rig secrets ci NAME...    the GitHub Actions steps that fetch NAME... for a job",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			return printSecretsStatus(cmd.Context(), cmd.OutOrStdout(), resolveRoot(cwd))
		},
	}
	cmd.AddCommand(newSecretsCICmd())
	return cmd
}

// printSecretsStatus is the report `rig secrets` prints and `rig info` shares:
// the provider and project, each context's config, what a local run here
// does, and the CLI's readiness.
func printSecretsStatus(ctx context.Context, w io.Writer, root string) error {
	setup, err := secrets.LoadLayered(root, config.GlobalPath())
	if err != nil {
		fmt.Fprintln(w, warnStyle.Render("  ! "+err.Error()))
		return err
	}
	if setup == nil {
		fmt.Fprintln(w, dimStyle.Render("  (no secrets manager — add \"doppler\": { \"project\": …, \"dev\": … } to .rig.json)"))
		return nil
	}
	p := setup.Provider
	fmt.Fprintf(w, "  provider:  %s\n", p.Title())
	fmt.Fprintf(w, "  project:   %s\n", setup.Settings.Project)
	for _, c := range []secrets.Context{secrets.Dev, secrets.CI, secrets.Release} {
		fmt.Fprintf(w, "  %-9s  %s\n", string(c)+":", orNone(setup.Settings.Config(c)))
	}
	switch scope, ok := setup.Scope(secrets.Dev); {
	case secrets.InCI():
		fmt.Fprintln(w, dimStyle.Render("  local runs: none here — this is CI, where each step takes its secrets from the workflow"))
	case !ok:
		fmt.Fprintln(w, dimStyle.Render("  local runs: no dev config, so rig wraps nothing"))
	default:
		fmt.Fprintf(w, "  local runs: %s\n", strings.Join(p.Wrap(scope, []string{"<command>"}), " "))
		if err := p.Ready(ctx, root); err != nil {
			fmt.Fprintln(w, warnStyle.Render("  ! "+err.Error()))
		} else {
			fmt.Fprintln(w, "  cli:        "+OkStyle.Render("signed in"))
		}
	}
	return nil
}

func newSecretsCICmd() *cobra.Command {
	var (
		context     string
		stepID      string
		tokenSecret string
		indent      int
	)
	cmd := &cobra.Command{
		Use:   "ci NAME...",
		Short: "Print the GitHub Actions steps that fetch a job's secrets",
		Long: "ci prints three steps to paste into a job's `steps:` — a check that the token\n" +
			"secret is set, the fetch (dopplerhq/secrets-fetch-action pinned by commit, with no\n" +
			"inject-env-vars), and a guard that fails the job when the token reads another\n" +
			"project/config or any NAME is missing or empty. Then each step that needs a\n" +
			"secret takes only that one:\n\n" +
			"  env:\n    POSTGRES_URL: ${{ steps.doppler.outputs.POSTGRES_URL }}\n\n" +
			"Never put a secret in a job-level env: every step, test code included, would\n" +
			"see it. Regenerate when the list changes rather than editing the steps.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			setup, err := secrets.LoadLayered(resolveRoot(cwd), config.GlobalPath())
			if err != nil {
				return err
			}
			if setup == nil {
				return errors.New("no secrets manager in .rig.json — add \"doppler\": { \"project\": …, \"ci\": … } first")
			}
			stepper, ok := setup.Provider.(secrets.CIStepper)
			if !ok {
				return fmt.Errorf("rig can't write CI steps for %s yet", setup.Provider.Title())
			}
			scope, ok := setup.Scope(secrets.Context(context))
			if !ok {
				return fmt.Errorf(".rig.json sets no %s.%s config", setup.Provider.Name(), context)
			}
			indent = secrets.NormalizeIndent(indent)
			yaml, err := stepper.CISteps(secrets.CIOptions{
				Scope: scope, Names: args, StepID: stepID, TokenSecret: tokenSecret, Indent: indent,
			})
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), yaml)
			// The consuming steps' env goes to stderr, so `> steps.yml` keeps
			// only what belongs in the job's step list.
			fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render("\nthen, on each step that needs a secret, only the ones it uses:"))
			fmt.Fprint(cmd.ErrOrStderr(), dimStyle.Render(secrets.CIEnv(stepID, args, indent+2)))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&context, "context", string(secrets.CI), "which .rig.json config the token reads: ci or release")
	f.StringVar(&stepID, "id", secrets.DefaultStepID, "the fetch step's id (steps.<id>.outputs.NAME)")
	f.StringVar(&tokenSecret, "token-secret", secrets.DefaultTokenSecret, "the GitHub Actions secret holding the service token")
	f.IntVar(&indent, "indent", secrets.DefaultIndent, "column of each step's leading '-'")
	return cmd
}
