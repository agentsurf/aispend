package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/prabhuvmk/aispend/internal/catalog"
	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/cred"
	"github.com/prabhuvmk/aispend/internal/store"
)

func newPurgeCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete everything aispend has stored, and say what was removed",
		Long: `purge removes aispend's database, its keychain entries, and its state directory.

It prints exactly what it deleted. Being able to say "when you are done, run
this and every trace is gone — here is what it removes" is worth more than the
thirty lines it costs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, caps := cmd.OutOrStdout(), capsFor(cmd)

			paths, err := config.Resolve()
			if err != nil {
				return err
			}

			plan, err := planPurge(paths)
			if err != nil {
				return err
			}
			if len(plan) == 0 {
				fmt.Fprintf(out, "\n  %s\n", caps.Dim("nothing to remove — aispend has stored nothing on this machine"))
				return nil
			}

			fmt.Fprintf(out, "\n  This will permanently delete:\n\n")
			for _, item := range plan {
				fmt.Fprintf(out, "    %s\n", item)
			}

			if !force {
				ok, err := confirm(cmd, "\n  Type 'yes' to confirm: ")
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintf(out, "\n  %s\n", caps.Dim("nothing was removed"))
					return nil
				}
			}

			removed, err := doPurge(paths)
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "\n  %s removed:\n\n", caps.OK())
			for _, item := range removed {
				fmt.Fprintf(out, "    %s\n", item)
			}
			fmt.Fprintf(out, "\n  %s\n", caps.Dim("aispend has nothing stored on this machine"))
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip the confirmation prompt")
	return cmd
}

// planPurge describes what would be deleted, so the user sees it before
// deciding rather than after.
func planPurge(paths config.Paths) ([]string, error) {
	var plan []string

	if state, err := config.Stat(paths.DB); err == nil && state.Exists {
		summary := config.Display(paths.DB)
		if db, err := store.Open(paths.DB); err == nil {
			if h, err := db.Health(); err == nil {
				summary = fmt.Sprintf("%s  (%d facts, %d connections)",
					config.Display(paths.DB), h.Facts, h.Connections)
			}
			db.Close()
		}
		plan = append(plan, summary)
	}

	for _, v := range catalog.Vendors() {
		if cred.Stored(v.ID) {
			plan = append(plan, fmt.Sprintf("%s's key in your OS keychain", v.Name))
		}
	}

	for _, p := range []string{paths.Owners, paths.Config} {
		if state, err := config.Stat(p); err == nil && state.Exists {
			plan = append(plan, config.Display(p))
		}
	}
	if state, err := config.Stat(paths.Raw); err == nil && state.Exists {
		plan = append(plan, config.Display(paths.Raw)+"  (saved raw responses)")
	}
	if len(plan) > 0 {
		plan = append(plan, config.Display(paths.Dir)+"  (the directory itself)")
	}
	return plan, nil
}

func doPurge(paths config.Paths) ([]string, error) {
	var removed []string

	for _, v := range catalog.Vendors() {
		gone, err := cred.Delete(v.ID)
		if err != nil {
			return removed, err
		}
		if gone {
			removed = append(removed, fmt.Sprintf("%s's key from your OS keychain", v.Name))
		}
	}

	// Remove the directory last and whole, so nothing is left behind by a file
	// this build does not know about — a raw dump from an older version, say.
	entries, _ := os.ReadDir(paths.Dir)
	for _, e := range entries {
		removed = append(removed, config.Display(filepath.Join(paths.Dir, e.Name())))
	}
	if err := os.RemoveAll(paths.Dir); err != nil {
		return removed, fmt.Errorf("could not remove %s: %w", config.Display(paths.Dir), err)
	}
	removed = append(removed, config.Display(paths.Dir))
	return removed, nil
}

// confirm asks for an explicit yes. Non-interactive callers must pass --force
// rather than have the prompt silently succeed.
func confirm(cmd *cobra.Command, prompt string) (bool, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, fmt.Errorf(
			"purge needs confirmation and stdin is not a terminal\n\n  Use: aispend purge --force")
	}
	fmt.Fprint(cmd.OutOrStdout(), prompt)

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, nil
	}
	return strings.EqualFold(strings.TrimSpace(line), "yes"), nil
}
