package commands

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// ErrNotSetUp reports a workspace with no changeset config, off a terminal (so
// the inline setup offer cannot ask). Anything that asked for real work — `add`,
// `status`, any invocation carrying args or flags — surfaces it as the failure
// it is. BareRunE softens it for a bare invocation only.
var ErrNotSetUp = errors.New("not set up here yet")

// BareRunE wraps the handler a tool's root command runs when invoked with no
// verb, so that landing in an unconfigured directory is orientation rather than
// a failure.
//
// `changerig` and `shiprig` route their bare invocation to `add` and `status`,
// which need a configured workspace and error without one. Running the binary
// anywhere else therefore exited 1 — while `rig` and `clauderig` exit 0 — and
// that is the first thing anything probing the binary sees. winget's validation
// VM does exactly that after installing a package, logs the non-zero exit
// against the install, and labels the submission `Validation-Executable-Error`:
// it did so for ChangeRig in 1.4.0 (16 days) and again in 1.5.1.
//
// Only a truly bare run is softened. With args or flags (`changerig -m "…"`) the
// user asked for something specific and still gets the error and a non-zero
// exit, as do `add` and `status` invoked by name — which is what a CI gate
// should call, and what keeps "no changesets" a failure where that matters.
// Every other error passes through untouched.
func BareRunE(run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		err := run(cmd, args)
		if err == nil || !errors.Is(err, ErrNotSetUp) {
			return err
		}
		if len(args) > 0 || cmd.Flags().NFlag() > 0 {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render(err.Error()))
		return nil
	}
}
