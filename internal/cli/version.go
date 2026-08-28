package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/buildinfo"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version and build information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "aispend %s (%s, %s, %s)\n",
				buildinfo.Version, buildinfo.Commit, buildinfo.GoVersion(), buildinfo.Platform())
			return nil
		},
	}
}
