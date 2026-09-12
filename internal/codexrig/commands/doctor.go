package commands

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/doctorui"
	"github.com/rigsmith/rigsmith/internal/codexrig/doctor"
	"github.com/spf13/cobra"
)

// NewDoctorCmd builds `codexrig doctor`.
func NewDoctorCmd(version string) *cobra.Command {
	var fixAll bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that the backup is actually working",
		Long: "Every way this tool fails is quiet. A hook that is installed but untrusted\n" +
			"runs nothing and says nothing. A rejected push leaves a machine looking\n" +
			"synced. A remote that stopped being private looks exactly like one that\n" +
			"never was. This is where those turn into sentences.\n\n" +
			"It exits non-zero while anything is still failing, so it can gate a script.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			// Bound the checks that touch the network or another process, so
			// doctor cannot hang. The FIXES run on the uncapped context: a
			// repair is allowed to take as long as it takes.
			ctx, cancel := context.WithTimeout(cmd.Context(), 40*time.Second)
			env := doctor.NewEnv(ctx, version)
			sections := doctor.Run(ctx, env)
			cancel()

			fmt.Fprintf(out, "%s   %s · codexrig %s\n\n", HeaderStyle.Render("codexrig doctor"), runtime.GOOS, version)
			doctorui.RenderSections(out, sections)
			doctorui.RenderSummary(out, sections)

			fails := doctorui.RunFixes(cmd, sections, doctorui.Options{
				Accent: brand.AccentCodex, FixAll: fixAll, Interactive: interactive(),
			})
			if fails > 0 {
				// Not os.Exit: `codexrig ui` dispatches this command in-process,
				// so exiting here takes the whole process down mid-render and
				// skips every deferred cleanup. main already maps a returned
				// error to exit code 1, so the gate is unchanged.
				return errCheckFailed
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fixAll, "fix", false, "apply every fixable issue without prompting")
	return cmd
}
