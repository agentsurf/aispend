package cli

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/cred"
	"github.com/prabhuvmk/aispend/internal/store"
	"github.com/prabhuvmk/aispend/internal/ui"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the local install: paths, permissions, database, credentials",
		Long: `doctor reports what aispend can see on this machine.

Run it first if anything looks wrong. It makes no network calls in this version;
later it will also report what each configured credential could actually read.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := resolvePaths()
			if err != nil {
				return err
			}
			out, caps := cmd.OutOrStdout(), capsFor(cmd)

			if err := reportPaths(out, paths, caps); err != nil {
				return err
			}
			fmt.Fprintln(out)
			if err := reportDB(out, paths, caps); err != nil {
				return err
			}
			fmt.Fprintln(out)
			return reportCredentials(out, caps)
		},
	}
}

// reportPaths prints the paths block. Each line says where a thing is and
// whether it is in the state aispend expects, because "missing" and "there but
// world-readable" need different reactions from the reader.
func reportPaths(w io.Writer, paths config.Paths, caps ui.Caps) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	rows := []struct {
		label    string
		path     string
		want     os.FileMode
		optional bool
	}{
		{"config", paths.Dir + string(os.PathSeparator), config.DirPerm, false},
		{"db", paths.DB, config.FilePerm, false},
		{"owners", paths.Owners, config.FilePerm, true},
	}

	for i, r := range rows {
		lead := ""
		if i == 0 {
			lead = "paths"
		}

		state, err := config.Stat(r.path)
		if err != nil {
			return err
		}

		var note string
		switch {
		case !state.Exists && r.optional:
			note = "missing " + caps.Sep() + " optional"
		case !state.Exists:
			note = "missing"
		case state.Perm == r.want:
			note = fmt.Sprintf("%#o %s", state.Perm, caps.OK())
		default:
			note = fmt.Sprintf("%#o %s expected %#o", state.Perm, caps.Warn(), r.want)
		}

		fmt.Fprintf(tw, "  %s\t%s\t%s\t(%s)\n", lead, r.label, config.Display(r.path), note)
	}

	return tw.Flush()
}

// reportDB is doctor's database block: schema version, how much is stored, and
// what range it covers.
func reportDB(w io.Writer, paths config.Paths, caps ui.Caps) error {
	state, err := config.Stat(paths.DB)
	if err != nil {
		return err
	}
	if !state.Exists {
		// Not an error, and not a zero: on a first run there is genuinely
		// nothing here yet, and saying so is more useful than an empty table.
		fmt.Fprintf(w, "  db     no database yet %s run: aispend scan\n", caps.Sep())
		return nil
	}

	db, err := store.Open(paths.DB)
	if err != nil {
		return err
	}
	defer db.Close()

	h, err := db.Health()
	if err != nil {
		return err
	}

	sep := " " + caps.Sep() + " "
	fmt.Fprintf(w, "  db     %s%sschema v%d%s%d facts%s%d connections\n",
		"ok", sep, h.SchemaVersion, sep, h.Facts, sep, h.Connections)

	if h.CoveredFrom != "" {
		fmt.Fprintf(w, "         covered  %s %s %s\n", h.CoveredFrom, caps.Dash(), h.CoveredTo)
	}
	return nil
}

// reportCredentials is doctor's credentials block: which vendors have a key and
// where it came from. It reports only what was found locally — whether the key
// actually works is a network question, and it gets its own block once the
// collectors exist.
func reportCredentials(w io.Writer, caps ui.Caps) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	for i, c := range cred.ResolveAll() {
		lead := ""
		if i == 0 {
			lead = "credentials"
		}

		if c.Empty() {
			// No credential is not a failure and not a zero: it is a vendor
			// this machine has not been told about.
			fmt.Fprintf(tw, "  %s\t%s\t%s\tno credential\n", lead, c.Vendor, caps.Dash())
			continue
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s %s\t(%s)\n", lead, c.Vendor, c.Source, c.Ref, c.Display())
	}
	return tw.Flush()
}
