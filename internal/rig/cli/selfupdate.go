// `rig self-update [--check]` — updates the whole rigsmith family to the latest
// release. The verb itself is shared by every tool (core/selfupdate); rig wires
// in its own command runner so --dry-run and the command echo apply.
package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/rigsmith/rigsmith/core/selfupdate"
)

// version is the build version, stamped at release time via
//
//	-ldflags "-X github.com/rigsmith/rigsmith/internal/rig/cli.version={{.Version}}"
//
// (see .goreleaser.yaml). Source builds stay "dev", which self-update treats
// as "not an installed release" and degrades gracefully.
var version = "dev"

func newSelfUpdateCmd() *cobra.Command {
	return selfupdate.NewCmd(selfupdate.Options{
		Tool:    "rig",
		Version: version,
		Run: func(cmd *cobra.Command, argv []string) error {
			cwd, _ := os.Getwd()
			return runCommandAsIs(cmd, cwd, argv)
		},
	})
}
