package cli

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/buildinfo"
	"github.com/prabhuvmk/aispend/internal/store"
	"github.com/prabhuvmk/aispend/internal/timerange"
)

var (
	flagJSON bool
	flagCSV  bool
)

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write collected data as JSON or CSV. No network.",
		Long: `export writes what has been collected in a form other tools can read.

Amounts are integer micros in JSON and decimal dollars in CSV, and token counts
are exact in both — the humanised forms are for the terminal only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFlags(); err != nil {
				return err
			}
			r, err := timerange.Parse(flagSince, time.Now())
			if err != nil {
				return err
			}
			paths, err := resolvePaths()
			if err != nil {
				return err
			}
			db, err := store.Open(paths.DB)
			if err != nil {
				return err
			}
			defer db.Close()

			filter := store.Filter{From: r.FromDay(), To: r.ToDay(), Vendor: flagVendor}
			out := cmd.OutOrStdout()

			switch {
			case flagCSV:
				return exportCSV(out, db, filter)
			default:
				return exportJSON(out, db, filter, r)
			}
		},
	}

	cmd.Flags().StringVar(&flagSince, "since", "30d", "window to export: 7d, 30d, 90d, or a date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&flagVendor, "vendor", "", "export one vendor only")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "write JSON (the default)")
	cmd.Flags().BoolVar(&flagCSV, "csv", false, "write CSV, for a spreadsheet")
	return cmd
}

// exportRow is one fact as it appears in an export.
//
// Money is raw integer micros and token counts are exact: the humanised forms
// exist for a terminal, and anything reading this file is going to do
// arithmetic on it. No field carries credential material — the principal is the
// vendor's own key *identifier*, never a key.
type exportRow struct {
	Vendor          string `json:"vendor"`
	Day             string `json:"day"`
	Project         string `json:"project,omitempty"`
	Principal       string `json:"principal,omitempty"`
	Model           string `json:"model,omitempty"`
	InputUnits      int64  `json:"input_units"`
	OutputUnits     int64  `json:"output_units"`
	CachedUnits     int64  `json:"cached_units"`
	CacheWriteUnits int64  `json:"cache_write_units"`
	OtherUnits      int64  `json:"other_units"`
	UnitKind        string `json:"unit_kind"`
	AmountMicros    int64  `json:"amount_micros"`
	AmountBasis     string `json:"amount_basis"`
	PriceVersion    string `json:"price_version,omitempty"`
	Revision        int    `json:"revision"`
}

func exportJSON(w io.Writer, db *store.DB, filter store.Filter, r timerange.Range) error {
	rows, err := db.Facts(filter)
	if err != nil {
		return err
	}
	totals, err := db.Totals(filter)
	if err != nil {
		return err
	}

	doc := struct {
		Schema int         `json:"schema"`
		Agent  string      `json:"agent"`
		From   string      `json:"from"`
		To     string      `json:"to"`
		Total  int64       `json:"total_micros"`
		Facts  int         `json:"fact_count"`
		Priced int         `json:"priced_fact_count"`
		Rows   []exportRow `json:"facts"`
	}{
		Schema: 1, Agent: buildinfo.Version,
		From: r.FromDay(), To: r.ToDay(),
		Total: totals.Micros, Facts: totals.Facts, Priced: totals.Priced,
		Rows: make([]exportRow, 0, len(rows)),
	}
	for _, f := range rows {
		doc.Rows = append(doc.Rows, exportRow{
			Vendor: f.Vendor, Day: f.Day, Project: f.WorkspaceRef,
			Principal: f.PrincipalRef, Model: f.ModelRef,
			InputUnits: f.InputUnits, OutputUnits: f.OutputUnits,
			CachedUnits: f.CachedUnits, CacheWriteUnits: f.CacheWriteUnits,
			OtherUnits: f.OtherUnits, UnitKind: f.UnitKind,
			AmountMicros: f.AmountMicros, AmountBasis: string(f.AmountBasis),
			PriceVersion: f.PriceVersion, Revision: f.Revision,
		})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func exportCSV(w io.Writer, db *store.DB, filter store.Filter) error {
	rows, err := db.Facts(filter)
	if err != nil {
		return err
	}

	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write([]string{
		"vendor", "day", "project", "principal", "model",
		"input_units", "output_units", "cached_units", "cache_write_units", "other_units",
		"unit_kind", "amount_usd", "amount_micros", "amount_basis", "price_version", "revision",
	}); err != nil {
		return err
	}

	for _, f := range rows {
		if err := cw.Write([]string{
			f.Vendor, f.Day, f.WorkspaceRef, f.PrincipalRef, f.ModelRef,
			strconv.FormatInt(f.InputUnits, 10), strconv.FormatInt(f.OutputUnits, 10),
			strconv.FormatInt(f.CachedUnits, 10), strconv.FormatInt(f.CacheWriteUnits, 10),
			strconv.FormatInt(f.OtherUnits, 10), f.UnitKind,
			// Dollars for the spreadsheet, micros alongside for anything that
			// needs to add them up without a rounding argument.
			usdColumn(f.AmountMicros),
			strconv.FormatInt(f.AmountMicros, 10),
			string(f.AmountBasis), f.PriceVersion,
			strconv.Itoa(f.Revision),
		}); err != nil {
			return err
		}
	}
	return cw.Error()
}

// usdColumn renders micros as a plain decimal, with no currency symbol or
// thousands separator: a spreadsheet needs a number, not a formatted string.
func usdColumn(micros int64) string {
	neg := micros < 0
	if neg {
		micros = -micros
	}
	s := fmt.Sprintf("%d.%06d", micros/1_000_000, micros%1_000_000)
	if neg {
		return "-" + s
	}
	return s
}
