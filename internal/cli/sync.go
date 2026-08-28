package cli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/timerange"
)

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Refresh collected data without printing a report",
		Long: `sync collects from every connected vendor and prints nothing but progress.

It exists so a report can be explored offline afterwards with usage, and so a
scheduled refresh has a command that produces no output worth reading.

The window always includes a trailing re-pull, because vendors restate usage for
days they have already reported. A restated day is stored as a new revision, so
the earlier figure is still on disk.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := timerange.Parse(flagSince, time.Now())
			if err != nil {
				return err
			}
			return runScan(cmd, cmd.OutOrStdout(), capsFor(cmd), r, true)
		},
	}
	cmd.Flags().StringVar(&flagSince, "since", "7d", "window to refresh: 7d, 30d, 90d, or a date (YYYY-MM-DD)")
	return cmd
}
