package commands

import (
	"errors"
	"fmt"

	"github.com/rigsmith/rigsmith/internal/codexrig/account"
	"github.com/spf13/cobra"
)

// switchJSON is the object `switch --json` writes on stdout. Everything a human
// would read goes to stderr in that mode, so stdout carries exactly one object.
type switchJSON struct {
	Switched bool   `json:"switched"`
	DryRun   bool   `json:"dryRun,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to"`
	ToEmail  string `json:"toEmail,omitempty"`
	Backup   string `json:"backup,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
	Blocking []struct {
		PID  int    `json:"pid"`
		Kind string `json:"kind"`
	} `json:"blocking,omitempty"`
}

func newAccountSwitchCmd() *cobra.Command {
	var dryRun, force, kill, asJSON bool
	cmd := &cobra.Command{
		Use:   "switch [<id|email|alias>]",
		Short: "Change which login plain `codex` uses",
		Long: "Swaps this machine's Codex credential for another tracked account's, so an\n" +
			"ordinary `codex` runs as that login.\n\n" +
			"It refuses while Codex is running: a live session holds the credential it\n" +
			"started with, and swapping underneath it leaves that session unable to refresh.\n" +
			"--kill ends them first; --force swaps anyway and accepts that cost.\n\n" +
			"The displaced credential is stored back under its own account and backed up,\n" +
			"so switching away and back loses nothing. If you only want to RUN as another\n" +
			"account, `codexrig account run` does that without touching the machine at all.",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
			// With --json, every human line moves to stderr so stdout stays one
			// object; without it, prose goes to stdout as usual.
			prose := out
			if asJSON {
				prose = errOut
			}
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}

			doc := switchJSON{DryRun: dryRun, To: ref}
			fail := func(reason string, err error, blocking []account.Instance) error {
				if asJSON {
					doc.Reason, doc.Message = reason, err.Error()
					for _, b := range blocking {
						doc.Blocking = append(doc.Blocking, struct {
							PID  int    `json:"pid"`
							Kind string `json:"kind"`
						}{b.PID, b.Kind})
					}
					_ = writeJSON(out, doc)
				}
				return err
			}

			s, err := openStore()
			if err != nil {
				return fail(reasonFailed, err, nil)
			}
			a, err := resolveAccountRef(s, ref)
			if err != nil {
				return fail(classifyResolve(err), err, nil)
			}
			doc.To, doc.ToEmail = a.ID, a.Email

			res, err := s.Switch(a, account.SwitchOptions{Force: force, Kill: kill, DryRun: dryRun})
			doc.From, doc.Backup = res.From, res.Backup
			if err != nil {
				if errors.Is(err, account.ErrCodexBusy) {
					fmt.Fprintf(prose, "%s\n", ErrStyle.Render("Refusing to switch: Codex is running."))
					fmt.Fprintf(prose, "  %s\n", DimStyle.Render("close it · --kill to end it first · --force to swap anyway · or use `codexrig account run` instead"))
					for _, b := range res.Blocking {
						fmt.Fprintf(prose, "  • pid %d  %s\n", b.PID, DimStyle.Render(b.Kind))
					}
					return fail(reasonLiveSessions, err, res.Blocking)
				}
				return fail(classifySwitch(err), err, res.Blocking)
			}

			if dryRun {
				fmt.Fprintln(prose, HeaderStyle.Render("switch --dry-run"))
				if res.From != "" {
					fmt.Fprintf(prose, "  would store the displaced credential back under %s\n", res.From)
				}
				fmt.Fprintf(prose, "  would set the live login → %s\n", a.Title())
				if len(res.Blocking) > 0 {
					fmt.Fprintf(prose, "  %s\n", WarnStyle.Render(fmt.Sprintf("%d Codex process(es) running — the switch would refuse without --kill or --force", len(res.Blocking))))
				} else {
					fmt.Fprintf(prose, "  %s\n", DimStyle.Render("no live sessions — the switch would proceed"))
				}
				if asJSON {
					return writeJSON(out, doc)
				}
				return nil
			}

			if !res.Switched {
				fmt.Fprintf(prose, "%s %s\n", DimStyle.Render("already live:"), a.Title())
				doc.Reason = reasonAlreadyLive
				if asJSON {
					return writeJSON(out, doc)
				}
				return nil
			}
			if res.Backup != "" {
				fmt.Fprintf(prose, "%s %s\n", DimStyle.Render("backed up the displaced credential →"), res.Backup)
			}
			fmt.Fprintf(prose, "%s %s\n", OkStyle.Render("Switched to"), a.Title())
			doc.Switched = true
			if asJSON {
				return writeJSON(out, doc)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "report what would change, change nothing")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "swap even while Codex is running")
	cmd.Flags().BoolVarP(&kill, "kill", "k", false, "end running Codex processes first, then swap")
	cmd.Flags().BoolVar(&asJSON, "json", false, "report the outcome as JSON, including refusals")
	return cmd
}

func classifySwitch(err error) string {
	switch {
	case errors.Is(err, account.ErrCodexBusy):
		return reasonLiveSessions
	case errors.Is(err, account.ErrProcessScan):
		return reasonProcessScan
	case errors.Is(err, account.ErrStoredNoTokens):
		return reasonNoTokens
	case errors.Is(err, account.ErrAmbiguousRef):
		return reasonAmbiguous
	case errors.Is(err, account.ErrNoSuchAccount), errors.Is(err, account.ErrNoAccounts):
		return reasonNoSuchAccount
	default:
		return reasonFailed
	}
}
