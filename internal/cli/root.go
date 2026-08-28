package cli

import (
	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/buildinfo"
	"github.com/prabhuvmk/aispend/internal/dbg"
)

var (
	flagDebug   bool
	flagNoColor bool
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "aispend",
		Short: "Provider-agnostic AI spend analytics, from the command line",
		Long: `aispend collects AI usage and cost from your vendors and reports it locally.

Nothing leaves this machine. Credentials are read from the environment or your OS
keychain and are never written to the database, a config file, or any output.`,
		SilenceUsage:  true,
		SilenceErrors: false,
		Version:       buildinfo.Version,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			dbg.SetEnabled(flagDebug)
			dbg.Printf("aispend %s (%s, %s, %s)", buildinfo.Version, buildinfo.Commit,
				buildinfo.GoVersion(), buildinfo.Platform())
			dbg.Printf("command %q args %v", cmd.CommandPath(), args)
		},
	}

	root.PersistentFlags().BoolVar(&flagDebug, "debug", false, "print internal detail to stderr")
	root.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "disable colour output")

	root.AddCommand(newVersionCmd())

	return root
}

// Execute runs the CLI. main() turns a non-nil return into exit code 1.
func Execute() error {
	return newRootCmd().Execute()
}
