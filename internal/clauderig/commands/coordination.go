package commands

import (
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
	"github.com/spf13/cobra"
)

// coordinatedCommand holds ownership across the entire read/decide/write
// operation, including confirmation, backups and journalling. Cobra validates
// arguments before RunE; help and read-only commands never acquire ownership.
func coordinatedCommand(cmd *cobra.Command) *cobra.Command {
	note := "Waits up to 15 seconds for another staging operation; retry if the store stays busy."
	if cmd.Long == "" {
		cmd.Long = cmd.Short
	}
	cmd.Long += "\n\n" + note
	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		staging, err := config.StagingDir()
		if err != nil {
			return err
		}
		previous := cmd.Context()
		ctx, release, err := storelock.Acquire(previous, staging, service.StoreWait)
		if err != nil {
			return err
		}
		defer release()
		cmd.SetContext(ctx)
		defer cmd.SetContext(previous)
		return run(cmd, args)
	}
	return cmd
}
