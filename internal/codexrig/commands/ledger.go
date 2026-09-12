package commands

import (
	"fmt"
	"sort"
	"time"

	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/ledger"
	"github.com/spf13/cobra"
)

// NewLedgerCmd builds `codexrig ledger`: what the permanent index remembers,
// including sessions whose bodies have aged out of the sync window.
func NewLedgerCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ledger",
		Short: "What codexrig remembers, including sessions that have aged out",
		Long: "The synced tree is a rolling window; this is not. Every session codexrig has\n" +
			"ever staged leaves a row here, so a search for an old conversation can say\n" +
			"\"this existed, on this date, in this directory\" rather than nothing at all.\n\n" +
			"The bodies of aged-out sessions are still in the repo's git history.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}
			rows := ledger.LoadAll(staging)
			if asJSON {
				list := make([]ledger.Entry, 0, len(rows))
				for _, e := range rows {
					list = append(list, e)
				}
				sort.Slice(list, func(i, j int) bool { return list[i].End.After(list[j].End) })
				return writeJSON(out, map[string]any{"total": len(list), "sessions": list})
			}
			if len(rows) == 0 {
				fmt.Fprintln(out, DimStyle.Render("nothing remembered yet — run `codexrig sync` with sessions on"))
				return nil
			}

			var oldest, newest time.Time
			byMachine := map[string]int{}
			for _, e := range rows {
				byMachine[e.RecordedBy]++
				if !e.End.IsZero() {
					if oldest.IsZero() || e.End.Before(oldest) {
						oldest = e.End
					}
					if e.End.After(newest) {
						newest = e.End
					}
				}
			}
			fmt.Fprintln(out, HeaderStyle.Render("Session ledger"))
			fmt.Fprintf(out, "  %d session(s) remembered\n", len(rows))
			if !oldest.IsZero() {
				fmt.Fprintf(out, "  %s\n", DimStyle.Render("spanning "+oldest.Local().Format("2006-01-02")+" → "+newest.Local().Format("2006-01-02")))
			}
			names := make([]string, 0, len(byMachine))
			for n := range byMachine {
				names = append(names, n)
			}
			sort.Strings(names)
			fmt.Fprintln(out)
			for _, n := range names {
				label := n
				if label == "" {
					label = DimStyle.Render("(unrecorded)")
				}
				fmt.Fprintf(out, "  %-20s %d\n", label, byMachine[n])
			}
			fmt.Fprintf(out, "\n  %s\n", DimStyle.Render("`codexrig search <text>` searches these too"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the index as JSON")
	return cmd
}
