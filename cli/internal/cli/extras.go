package cli

import "github.com/spf13/cobra"

// extraCmds returns the heavier standalone commands (coverage, kill, doctor).
// Wired in as they land so root.go stays stable.
func extraCmds() []*cobra.Command {
	return []*cobra.Command{
		newCoverageCmd(),
		newKillCmd(),
		newDoctorCmd(),
		newCdCmd(),
		newPublishCmd(),
		newDefaultCmd(),
		newSetupCmd(),
	}
}
