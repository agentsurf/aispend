package cli

import (
	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/ui"
)

// resolvePaths locates the state directory and makes sure it exists at the
// right mode. Every command that touches disk starts here.
func resolvePaths() (config.Paths, error) {
	paths, err := config.Resolve()
	if err != nil {
		return paths, err
	}
	if err := paths.EnsureDir(); err != nil {
		return paths, err
	}
	return paths, nil
}

// capsFor works out what the command's output destination can render.
func capsFor(cmd *cobra.Command) ui.Caps {
	return ui.Detect(cmd.OutOrStdout(), flagNoColor)
}
