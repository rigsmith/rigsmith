package commands

import (
	"fmt"

	"github.com/rigsmith/rigsmith/internal/codexrig/account"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/spf13/cobra"
)

func newAccountDoctorCmd() *cobra.Command {
	var asJSON, fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that the live Codex login and codexrig's record of it agree",
		Long: "Reads the machine's credential and codexrig's own pointer and reports whether\n" +
			"they name the same account.\n\n" +
			"There is deliberately less to check here than in clauderig. Codex keeps its\n" +
			"identity inside the credential rather than in a second file that can disagree\n" +
			"with it, so the only faults possible are a missing credential, an unreadable\n" +
			"one, one that cannot authenticate, and a pointer naming somebody else.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			s, err := openStore()
			if err != nil {
				return err
			}
			obs := s.Diagnose()

			if fix && obs.PointerEmail != "" {
				// The only automatic repair available: the credential is the
				// authority, so make the pointer agree with it. The credential
				// itself is never touched — a doctor that logs you into a
				// different account to make a label true is not a fix.
				if id, _ := s.LiveAccount(); id != "" {
					if err := s.SetActive(id); err != nil {
						return err
					}
					fmt.Fprintf(out, "%s active account now names the live login (%s)\n", OkStyle.Render("Repaired —"), id)
					obs = s.Diagnose()
				}
			}

			if asJSON {
				return writeJSON(out, obs)
			}

			fmt.Fprintln(out, HeaderStyle.Render("Codex identity"))
			switch {
			case obs.LoggedOut:
				fmt.Fprintf(out, "  %-14s %s\n", "credential", DimStyle.Render("(absent — run `codex login`)"))
			case obs.LiveErr != "":
				fmt.Fprintf(out, "  %-14s %s\n", "credential", ErrStyle.Render("unreadable: "+obs.LiveErr))
			default:
				fmt.Fprintf(out, "  %-14s %s\n", "credential", identityLine(obs))
				fmt.Fprintf(out, "  %-14s %s\n", "", DimStyle.Render("~/.codex/auth.json — what the server authenticates you as"))
			}
			if obs.PointerID != "" {
				fmt.Fprintf(out, "  %-14s %s\n", "codexrig", obs.PointerID)
			}
			if obs.EnvHome != "" {
				fmt.Fprintf(out, "  %-14s %s\n", codexhome.EnvHome, WarnStyle.Render(obs.EnvHome))
			}

			problems := obs.Problems()
			fmt.Fprintln(out)
			if len(problems) == 0 {
				fmt.Fprintf(out, "%s\n", OkStyle.Render("✓ the live login and codexrig's record agree"))
			} else {
				for _, p := range problems {
					fmt.Fprintf(out, "%s %s\n", ErrStyle.Render("✗"), p)
				}
				if !fix {
					fmt.Fprintf(out, "\n  %s\n", DimStyle.Render("`codexrig account doctor --fix` repoints codexrig at the live login (it never changes the credential)"))
				}
			}

			printStoredAccounts(cmd, obs)
			if len(problems) > 0 && !fix {
				return errCheckFailed
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the diagnosis as JSON")
	cmd.Flags().BoolVar(&fix, "fix", false, "repoint codexrig at whichever login the credential names")
	return cmd
}

func identityLine(o account.Observation) string {
	line := orDim(o.LiveEmail, "(no email — API-key login)")
	if o.LivePlan != "" {
		line += DimStyle.Render(" · " + o.LivePlan)
	}
	if o.LiveAuthMode != "" {
		line += DimStyle.Render(" · " + o.LiveAuthMode)
	}
	return line
}

func orDim(s, fallback string) string {
	if s == "" {
		return DimStyle.Render(fallback)
	}
	return s
}

func printStoredAccounts(cmd *cobra.Command, o account.Observation) {
	if len(o.Accounts) == 0 {
		return
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n%s\n", HeaderStyle.Render("Stored accounts"))
	for _, a := range o.Accounts {
		marker := "  "
		if a.Active {
			marker = AccentStyle.Render("→ ")
		}
		cred := OkStyle.Render("credential ✓")
		if !a.CredentialTokens {
			cred = ErrStyle.Render("credential ✗ cannot authenticate")
		}
		var home string
		switch a.Home {
		case account.SessionOK:
			home = OkStyle.Render("home ✓")
		case account.SessionNoTokens:
			home = ErrStyle.Render("home ✗ cannot authenticate")
		case account.SessionUnknown:
			home = WarnStyle.Render("home ? unreadable")
		default:
			home = DimStyle.Render("home —")
		}
		fmt.Fprintf(out, "%s%-34s %s  %s\n", marker, a.Title(), cred, home)
	}
}
