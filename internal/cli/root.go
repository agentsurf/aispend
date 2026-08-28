package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/buildinfo"
	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/dbg"
)

var (
	flagDebug   bool
	flagNoColor bool
)

// envDebug turns on debug output without a flag, for wrapper scripts and CI.
const envDebug = "AISPEND_DEBUG"

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
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Debug is applied before anything else so the settings resolution
			// itself is visible when you're debugging the settings.
			applyDebug(cmd, config.File{})

			paths, err := config.Resolve()
			if err != nil {
				return err
			}
			file, err := config.LoadFile(paths.Config)
			if err != nil {
				return err
			}
			applySettings(cmd, file)

			dbg.Printf("aispend %s (%s, %s, %s)", buildinfo.Version, buildinfo.Commit,
				buildinfo.GoVersion(), buildinfo.Platform())
			dbg.Printf("command %q args %v", cmd.CommandPath(), args)
			return nil
		},
	}

	root.PersistentFlags().BoolVar(&flagDebug, "debug", false, "print internal detail to stderr")
	root.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "disable colour output")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newConnectionsCmd())
	root.AddCommand(newDebugCmd())

	return root
}

// applySettings resolves each setting through flag > env > file > default.
//
// A flag the user did not type must not beat a file value just because its zero
// value happens to be false — cobra's Changed() is what separates "set to false"
// from "not mentioned", the same distinction the File pointers preserve.
func applySettings(cmd *cobra.Command, file config.File) {
	applyDebug(cmd, file)

	if !cmd.Flags().Changed("no-color") && file.NoColor != nil {
		flagNoColor = *file.NoColor
		dbg.Printf("no-color=%v from config file", flagNoColor)
	}
	// NO_COLOR itself is honoured inside ui.Detect, where every writer sees it.
}

func applyDebug(cmd *cobra.Command, file config.File) {
	switch {
	case cmd.Flags().Changed("debug"):
		// the flag wins, whichever way it was set
	case os.Getenv(envDebug) != "":
		if b, err := strconv.ParseBool(os.Getenv(envDebug)); err == nil {
			flagDebug = b
		}
	case file.Debug != nil:
		flagDebug = *file.Debug
	}
	dbg.SetEnabled(flagDebug)
}

// Execute runs the CLI. main() turns a non-nil return into exit code 1.
func Execute() error {
	if err := newRootCmd().Execute(); err != nil {
		// Errors already reached stderr through cobra; this keeps the shape for
		// the error-message pass in a later run.
		_ = fmt.Sprint(err)
		return err
	}
	return nil
}
