package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/catalog"
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
	cmd.Flags().BoolVar(&flagDetail, "detail", false, "show every row, with no truncation")
	cmd.Flags().StringVar(&flagVendor, "vendor", "", "report on one vendor only")
	return cmd
}

// renderUsage prints the headline number and the footer.
func renderUsage(w io.Writer, caps ui.Caps, db *store.DB, dest sink.Sink, r timerange.Range) error {
	filter := store.Filter{From: r.FromDay(), To: r.ToDay(), Vendor: flagVendor}

	t, err := db.Totals(filter)
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

	if err := writeGroup(w, caps, db, store.ByVendor, "BY VENDOR", filter, t); err != nil {
		return err
	}

	fmt.Fprintln(w)
	return writeFooter(w, caps, dest, r, t)
}

// maxRows is how many rows a summary view shows before collapsing the rest into
// a remainder line. Capping without a remainder would leave the column not
// reconciling to the total, which is the first thing a sceptical reader checks.
const maxRows = 10

// writeGroup renders one grouped table: descending by spend, shares paired with
// absolutes, a remainder line, and a totals row that visibly reconciles.
func writeGroup(w io.Writer, caps ui.Caps, db *store.DB, by store.GroupBy,
	title string, filter store.Filter, t store.Totals) error {

	groups, err := db.GroupBy(by, filter)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}

	fmt.Fprintf(w, "\n  %s\n", title)

	shown := groups
	var rest []store.Group
	if !flagDetail && len(groups) > maxRows {
		shown, rest = groups[:maxRows], groups[maxRows:]
	}

	rows := make([][3]string, 0, len(shown)+2)
	for _, g := range shown {
		rows = append(rows, [3]string{
			labelFor(by, g.Key, caps),
			fmtutil.MoneyOrUnknown(g.Micros, g.Priced > 0),
			sharePct(g.Micros, t.Micros, g.Priced > 0 && t.Micros > 0),
		})
	}

	if len(rest) > 0 {
		var micros int64
		var priced int
		for _, g := range rest {
			micros += g.Micros
			priced += g.Priced
		}
		rows = append(rows, [3]string{
			caps.Dim(fmt.Sprintf("…and %d more", len(rest))),
			fmtutil.MoneyOrUnknown(micros, priced > 0),
			sharePct(micros, t.Micros, priced > 0 && t.Micros > 0),
		})
	}

	width := 0
	for _, row := range rows {
		if n := len([]rune(stripANSI(row[0]))); n > width {
			width = n
		}
	}
	if width < 34 {
		width = 34
	}

	for _, row := range rows {
		pad := width - len([]rune(stripANSI(row[0])))
		fmt.Fprintf(w, "  %s%s  %12s  %7s\n", row[0], strings.Repeat(" ", pad), row[1], row[2])
	}

	// The totals row is what makes the column checkable by eye rather than
	// taken on trust.
	fmt.Fprintf(w, "  %s\n", caps.Dim(strings.Repeat(lineRune(caps), width+23)))
	fmt.Fprintf(w, "  %s  %12s\n", strings.Repeat(" ", width),
		fmtutil.MoneyOrUnknown(t.Micros, t.Priced > 0))
	return nil
}

// labelFor renders a group key, showing an absent dimension as an em dash
// rather than a blank cell that reads as a rendering bug.
func labelFor(by store.GroupBy, key string, caps ui.Caps) string {
	if key == "" {
		switch by {
		case store.ByProject:
			return caps.Dash() + " no project reported"
		case store.ByKey:
			return caps.Dash() + " no key reported"
		default:
			return caps.Dash()
		}
	}
	if by == store.ByVendor {
		if v, ok := catalog.Get(key); ok {
			return v.Name
		}
	}
	return key
}

// sharePct pairs a percentage with the absolute it came from, per design 6.2.
func sharePct(part, whole int64, known bool) string {
	if !known || whole == 0 {
		return fmtutil.Unknown
	}
	return fmt.Sprintf("%d%%", (part*100+whole/2)/whole)
}

func lineRune(caps ui.Caps) string {
	if caps.UTF8 {
		return "─"
	}
	return "-"
}

// stripANSI measures a styled string by its visible width.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
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
