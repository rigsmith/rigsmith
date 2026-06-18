package commands

import "github.com/spf13/cobra"

// NewRootCmd builds the changerig command tree: the changeset lifecycle plus the
// bare-invocation `add` shortcut and the interactive menu. main wires fang
// styling and the bare-TTY → menu routing around it; keeping the tree here lets
// consistency checks (core/cliguard) construct it without the runtime concerns.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "changerig",
		Aliases:       []string{"changeset"},
		Short:         "Changesets: capture intent, then version across every ecosystem",
		Long:          "changeRig manages changeset files and turns them into version bumps and\nchangelogs. One engine decides bumps, cascade, and changelog for .NET, Node,\nGo, and Rust alike.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	add := NewAddCmd()
	// With args/flags (e.g. `changerig -m "…"`), or off a TTY, bare `changerig`
	// behaves like `changerig add` — scripted/flag-driven use is unchanged.
	root.RunE = add.RunE
	root.Args = add.Args
	root.Flags().AddFlagSet(add.Flags())

	root.AddCommand(
		NewInitCmd(),
		add,
		NewStatusCmd(),
		NewBrowseCmd(),
		NewVersionCmd(),
		NewPreCmd(),
		NewInfoCmd(),
		NewConfigCmd(),
		NewChangelogCmd(),
		NewUICmd(MenuItem{Label: "Doctor", Desc: "check the changeset setup", Build: NewDoctorCmd}),
		NewDoctorCmd(),
	)
	return root
}
