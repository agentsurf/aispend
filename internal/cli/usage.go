package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/egress"
	"github.com/prabhuvmk/aispend/internal/fmtutil"
	"github.com/prabhuvmk/aispend/internal/sink"
	"github.com/prabhuvmk/aispend/internal/store"
	"github.com/prabhuvmk/aispend/internal/timerange"
	"github.com/prabhuvmk/aispend/internal/ui"
)

func newUsageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Report on what has already been collected. No network.",
		Long: `usage reports from the local database and makes no network calls.

Run scan once to collect, then explore with usage as often as you like — every
view is instant and costs nothing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			return renderUsage(cmd.OutOrStdout(), capsFor(cmd), db, destination(db), r)
		},
	}
	cmd.Flags().StringVar(&flagSince, "since", "30d", "window to report on: 7d, 30d, 90d, or a date (YYYY-MM-DD)")
	return cmd
}

// renderUsage prints the headline number and the footer.
func renderUsage(w io.Writer, caps ui.Caps, db *store.DB, dest sink.Sink, r timerange.Range) error {
	t, err := db.Totals(r.FromDay(), r.ToDay())
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\n  AI SPEND %s %s %s %s\n\n", caps.Sep(), r.Label, caps.Sep(), r.String())

	if t.Facts == 0 {
		fmt.Fprintf(w, "  %s\n\n", caps.Dim(
			"nothing collected for this window yet — run: aispend scan"))
		return nil
	}

	// A total of zero and a total aispend could not determine are different
	// facts. With no priced facts the headline is an em dash and a footnote,
	// never $0 — a tool that renders "we couldn't see this" as zero has
	// silently lied, and one occurrence found by a customer costs the account.
	fmt.Fprintf(w, "  %-40s %14s\n", "Total", fmtutil.MoneyOrUnknown(t.Micros, t.Priced > 0))

	if t.Priced < t.Facts {
		fmt.Fprintf(w, "  %s\n", caps.Dim(fmt.Sprintf(
			"%d of %d facts carry no cost yet %s tokens are collected, pricing is not wired up",
			t.Facts-t.Priced, t.Facts, caps.Sep())))
	}

	fmt.Fprintln(w)
	return writeFooter(w, caps, dest, r, t)
}

// writeFooter prints the trust block from design §6.1.
//
// Both lines are generated, not written: the Privacy claim comes from what the
// sinks say about themselves, and the host list from the allowlist the dialer
// enforces. Design §9.4 makes that a rule with a test behind it — get it wrong
// once, ship a build where scan quietly phones home, and you lose the only
// thing that makes a one-person company credible enough to be handed an admin
// key.
func writeFooter(w io.Writer, caps ui.Caps, dest sink.Sink, r timerange.Range, t store.Totals) error {
	line := strings.Repeat("─", 66)
	if !caps.UTF8 {
		line = strings.Repeat("-", 66)
	}
	fmt.Fprintf(w, "  %s\n", caps.Dim(line))

	fmt.Fprintf(w, "  %-9s %s\n", "Privacy", caps.Dim(privacyLine(dest)))
	fmt.Fprintf(w, "  %-9s %s\n", "Days", caps.Dim(daysLine(r, t)))
	return nil
}

// privacyLine describes where collected data went and which hosts were reached.
//
// It reports what this run actually did, not what the binary is permitted to
// do. An offline command says it used no network, and with fixture mode that
// claim is verifiable by unplugging the machine — which is the difference
// between a statement a security reviewer can check and marketing copy.
func privacyLine(dest sink.Sink) string {
	hosts := egress.Contacted()

	if !sink.Local(dest) {
		// A sink that leaves the machine must change this sentence on its own,
		// rather than relying on someone remembering to edit it.
		return "Collected data is sent to " + dest.Describe() + "." + contactedClause(hosts)
	}
	return "Nothing left this machine." + contactedClause(hosts)
}

func contactedClause(hosts []string) string {
	if len(hosts) == 0 {
		return " No network was used."
	}
	return " Contacted: " + strings.Join(hosts, ", ")
}

// daysLine states the timezone convention and warns about the range edges.
func daysLine(r timerange.Range, t store.Totals) string {
	verb := "have"
	if t.Days == 1 {
		verb = "has"
	}
	s := fmt.Sprintf("All dates UTC. %d of %d days in range %s data.", t.Days, r.Days(), verb)
	// Vendors restate recent days after the fact, so the newest end of any
	// window is provisional. Saying so is cheaper than explaining later why a
	// number moved.
	return s + " Vendors restate recent days, so the last 2 may still change."
}
