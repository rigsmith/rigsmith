package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rigsmith/rigsmith/internal/codexrig/account"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/spf13/cobra"
)

// Stable refusal codes. They are the script-facing half of `prepare` and
// `switch`: a caller branches on these, never on the prose beside them, so the
// message can be reworded without breaking anything.
const (
	reasonNoSuchAccount = "no-such-account"
	reasonUnmapped      = "unmapped-directory"
	reasonAmbiguous     = "ambiguous-account"
	reasonNoTokens      = "no-tokens"
	reasonHomeUnknown   = "session-unknown"
	reasonHomeDesync    = "profile-desync"
	reasonRelocated     = "relocated-home"
	reasonFailed        = "failed"
	reasonAlreadyLive   = "already-live"
	reasonLiveSessions  = "live-sessions"
	reasonProcessScan   = "process-scan-failed"
	reasonCodexBusy     = "codex-busy"
)

// prepareJSON is the object `prepare --json` writes on stdout.
//
// The directory is called configDir rather than home, deliberately. It is the
// generic name for "the directory this vendor's environment variable points at",
// and clauderig already spells it that way; a consumer that readies a profile
// for either tool reads one field, not one per vendor. The variable it belongs
// to is named alongside it in `env` so nothing has to know which rig answered.
type prepareJSON struct {
	Prepared  bool   `json:"prepared"`
	ID        string `json:"id,omitempty"`
	Email     string `json:"email,omitempty"`
	Alias     string `json:"alias,omitempty"`
	ConfigDir string `json:"configDir,omitempty"`
	Env       string `json:"env,omitempty"`
	Session   string `json:"session,omitempty"`
	Shared    bool   `json:"shared"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
}

// NewAccountPrepareCmd builds `codexrig account prepare`, the machine-readable
// half of `run`: it readies an account's isolated home and prints the path,
// leaving the launch to the caller.
//
// Exported because it is a contract, not an implementation detail: other
// programs shell out to it to launch Codex under a chosen account, and a
// contract only reachable through a package-private constructor is one nobody
// can test against.
func NewAccountPrepareCmd() *cobra.Command {
	var noShare, asJSON bool
	cmd := &cobra.Command{
		Use:   "prepare [<id|email|alias>]",
		Short: "Ready an account's isolated home and print its path",
		Long: "Does everything `run` does except start Codex: seeds the account's own\n" +
			"CODEX_HOME, links the shared setup in, verifies it can authenticate, and\n" +
			"prints the directory.\n\n" +
			"stdout carries the path and nothing else, so it can be captured directly:\n" +
			"    CODEX_HOME=$(codexrig account prepare work) codex\n" +
			"With --json it carries one object instead, including a stable `reason` when\n" +
			"the account could not be readied.",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}

			fail := func(reason string, err error) error {
				if asJSON {
					_ = writeJSON(out, prepareJSON{Reason: reason, Message: err.Error(), Shared: !noShare})
				}
				return err
			}

			s, err := openStore()
			if err != nil {
				return fail(reasonFailed, err)
			}
			a, err := resolveAccountRef(s, ref)
			if err != nil {
				return fail(classifyResolve(err), err)
			}
			home, err := s.EnsureHome(a, !noShare)
			if err != nil {
				return fail(classifyPrepare(err), err)
			}

			// Verify rather than assume. EnsureHome can succeed against a home
			// whose credential was replaced out from under it — someone ran
			// `codex login` inside it as a different account — and handing that
			// path back would run the caller's work under the wrong login while
			// reporting the right one.
			state := s.HomeStatus(a.ID)
			switch state {
			case account.SessionOK:
			case account.SessionUnknown:
				return fail(reasonHomeUnknown, fmt.Errorf("%s's home is there but its credential could not be read", a.Title()))
			default:
				return fail(reasonNoTokens, fmt.Errorf("%s's home cannot authenticate (%s) — run `codexrig account run %s` and log in", a.Title(), state, a.ID))
			}
			if got, err := s.HomeIdentity(a.ID); err == nil {
				if a.Email != "" && got.Email != "" && !strings.EqualFold(a.Email, got.Email) {
					return fail(reasonHomeDesync, fmt.Errorf("%s's home authenticates as %s — it is not the account it is filed under", a.Title(), got.Email))
				}
			}

			if asJSON {
				return writeJSON(out, prepareJSON{
					Prepared: true, ID: a.ID, Email: a.Email, Alias: a.Alias,
					ConfigDir: home, Env: codexhome.EnvHome, Session: state, Shared: !noShare,
				})
			}
			fmt.Fprintf(errOut, "%s %s\n", DimStyle.Render("prepared:"), a.Title())
			fmt.Fprintln(out, home)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noShare, "no-share", false, "give this account its own setup instead of sharing the machine's")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the outcome as JSON (one object on stdout; prose on stderr)")
	return cmd
}

// classifyResolve and classifyPrepare map an error to its stable code. They
// match on SENTINELS, never on message text, so rewording a message cannot
// silently change what a script sees.
func classifyResolve(err error) string {
	switch {
	case errors.Is(err, account.ErrUnmapped):
		return reasonUnmapped
	case errors.Is(err, account.ErrAmbiguousRef):
		return reasonAmbiguous
	case errors.Is(err, account.ErrNoSuchAccount), errors.Is(err, account.ErrNoAccounts):
		return reasonNoSuchAccount
	default:
		return reasonFailed
	}
}

func classifyPrepare(err error) string {
	switch {
	case errors.Is(err, account.ErrStoredNoTokens):
		return reasonNoTokens
	case errors.Is(err, account.ErrHomeUnreadable):
		return reasonHomeUnknown
	default:
		return reasonFailed
	}
}
