package cli

import "github.com/spf13/cobra"

// remoteCmd gives followed sources git's words: `remote` lists them,
// `remote add` follows one and `remote remove` stops following it. The
// archive is the repository, so its remotes are the sources it pulls from.
func (a *app) remoteCmd() *cobra.Command {
	ls := a.sourcesCmd()
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "List, add or remove followed sources (same as sources, follow, unfollow)",
		Args:  argsN(0, 0),
		RunE:  ls.RunE,
	}
	// `remote -v` needs no flag of its own: -v is the global --verbose.
	add := a.followCmd()
	add.Use = "add <source>"
	rm := a.unfollowCmd()
	rm.Use, rm.Aliases = "remove <source>", []string{"rm"}
	cmd.AddCommand(add, rm)
	return cmd
}
