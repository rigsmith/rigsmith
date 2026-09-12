package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/agentrig/ghrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/account"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/hooks"
	"github.com/spf13/cobra"
)

// NewInitCmd builds `codexrig init`.
func NewInitCmd() *cobra.Command {
	var remote, name string
	var installHooks, sessions, yes bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set this machine up: a private remote, and the hooks that keep it current",
		Long: "Writes ~/.codexrig/config.json, points it at a private git repo, and installs\n" +
			"the hooks that sync at the end of a turn and pull at the start of a session.\n\n" +
			"The remote must be PRIVATE, and codexrig verifies that rather than trusting it.\n" +
			"Your Codex credential is never synced, whatever the repo's settings say.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			if name == "" {
				name = config.ResolveName(cfg)
			}
			me := config.Detect(name)

			asked := false
			if !yes && interactive() && remote == "" {
				chosen, sess, hk, ferr := initForm(cmd, cfg)
				if ferr != nil {
					return ferr
				}
				remote, sessions, installHooks = chosen, sess, hk
				asked = true
			}

			if remote != "" {
				// The gate, always, on every path that sets a remote. A single
				// path that skips it is the path people take.
				if err := ghrepo.EnsurePrivate(cmd.Context(), remote); err != nil {
					return err
				}
				cfg.Remote = remote
			}
			// Only when the user actually answered — the form, or the flag
			// spelled out. `init --yes` and `init --remote` skip the form, and
			// assigning the flag's false default there turned session syncing
			// OFF on every re-run for anyone who had it on.
			if asked || cmd.Flags().Changed("sessions") {
				cfg.SyncSessions = sessions
			}
			// From here on, what the summary reports is what was saved — not
			// the flag's default on a path that never asked.
			sessions = cfg.SyncSessions
			if cfg.Machines == nil {
				cfg.Machines = map[string]config.Machine{}
			}
			// Only a machine with a real identity is registered. A placeholder
			// name creates a ghost every other machine then has to look at.
			if config.IdentityResolved(cfg) || name != config.UnresolvedName {
				cfg.Machines[me.Name] = me
			}

			dir, err := config.Dir()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
			if err := config.Save(cfg, dir); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("wrote"), dir+"/config.json")

			if installHooks {
				home, herr := codexhome.Default()
				if herr != nil {
					return herr
				}
				path := home + "/" + hooks.FileName
				added, updated, ierr := hooks.Install(path, hooks.SyncPlans())
				if ierr != nil {
					return ierr
				}
				if len(added)+len(updated) > 0 {
					fmt.Fprintf(out, "%s %s\n", OkStyle.Render("installed hooks"), strings.Join(append(added, updated...), ", "))
				}
				fmt.Fprintf(out, "  %s\n", WarnStyle.Render("Codex will not run them until they are trusted: `codexrig global trust`"))
			}

			// Say what is NOT covered, here, once, plainly. Someone who reads
			// "set up" and assumes their conversations are backed up has been
			// misled by omission.
			fmt.Fprintln(out)
			fmt.Fprintf(out, "  %s\n", HeaderStyle.Render("what travels"))
			fmt.Fprintf(out, "    %s\n", "config.toml, AGENTS.md, skills, prompts and rules")
			if sessions {
				fmt.Fprintf(out, "    %s\n", "session rollouts")
			} else {
				fmt.Fprintf(out, "    %s\n", DimStyle.Render("not session rollouts — `codexrig config set syncSessions true`"))
			}
			fmt.Fprintf(out, "    %s\n", DimStyle.Render("never auth.json, the databases, plugins or caches"))

			if _, aerr := account.ReadLive(); aerr != nil {
				fmt.Fprintf(out, "\n  %s\n", DimStyle.Render("not logged in to Codex yet — run `codex login`"))
			}
			fmt.Fprintf(out, "\n  %s\n", DimStyle.Render("next: `codexrig sync`"))
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "the private git repo to sync to")
	cmd.Flags().StringVar(&name, "name", "", "this machine's name in the device registry (default: its hostname)")
	cmd.Flags().BoolVar(&installHooks, "hooks", true, "install the sync hooks into your Codex home")
	cmd.Flags().BoolVar(&sessions, "sessions", false, "also carry session rollouts, not just configuration")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "take the defaults without asking")
	return cmd
}

// initForm is the interactive setup: pick a remote, decide what travels.
func initForm(cmd *cobra.Command, cfg *config.Config) (remote string, sessions, installHooks bool, err error) {
	installHooks = true
	chooseRemote := "existing"
	// An option that can only fail is not an option. Without gh, "create" used
	// to be offered and then fail the whole form on selection.
	options := []huh.Option[string]{huh.NewOption("I have a repo already", "existing")}
	if ghrepo.Available() {
		options = append(options, huh.NewOption("Create one with the gh CLI", "create"))
	}
	options = append(options, huh.NewOption("Nowhere yet — keep it local", "none"))
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("Where should your setup be backed up?").
				Description("It has to be a private repo. codexrig checks, and refuses a public one.").
				Options(options...).Value(&chooseRemote),
		),
		huh.NewGroup(
			huh.NewConfirm().Title("Carry session rollouts too?").
				Description("Your conversations, not just your configuration. They are large, and cross-machine\nresume is not a proven round trip yet — so this is backup rather than portability.").
				Affirmative("Yes").Negative("Config only").Value(&sessions),
			huh.NewConfirm().Title("Install the Codex hooks?").
				Description("Sync at the end of a turn, pull at the start of a session.").
				Affirmative("Yes").Negative("Not now").Value(&installHooks),
		),
	).WithTheme(brand.Theme(brand.AccentCodex)).WithKeyMap(huhEscKeyMap())
	if err := form.Run(); err != nil {
		return "", false, false, err
	}

	switch chooseRemote {
	case "none":
		return "", sessions, installHooks, nil
	case "create":
		if !ghrepo.Available() {
			return "", sessions, installHooks, fmt.Errorf("creating a repo needs the gh CLI — install it, or paste an existing repo's URL")
		}
		repoName := "codex-sync"
		nameForm := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("Repository name").Value(&repoName),
		)).WithTheme(brand.Theme(brand.AccentCodex)).WithKeyMap(huhEscKeyMap())
		if err := nameForm.Run(); err != nil {
			return "", sessions, installHooks, err
		}
		url, cerr := ghrepo.CreatePrivate(cmd.Context(), repoName)
		if cerr != nil {
			return "", sessions, installHooks, cerr
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", OkStyle.Render("created"), url)
		return url, sessions, installHooks, nil
	default:
		var url string
		urlForm := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("Repository URL").
				Description("github.com or gitlab.com, and private.").Value(&url),
		)).WithTheme(brand.Theme(brand.AccentCodex)).WithKeyMap(huhEscKeyMap())
		if err := urlForm.Run(); err != nil {
			return "", sessions, installHooks, err
		}
		return strings.TrimSpace(url), sessions, installHooks, nil
	}
}
