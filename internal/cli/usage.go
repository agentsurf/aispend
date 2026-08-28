package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/analytics"
	"github.com/prabhuvmk/aispend/internal/catalog"
	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/egress"
	"github.com/prabhuvmk/aispend/internal/fmtutil"
	"github.com/prabhuvmk/aispend/internal/owners"
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
			// Validate before opening anything or writing a line: a bad flag
			// that prints half a report first reads as a broken report rather
			// than a typo.
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

			return renderUsage(cmd.OutOrStdout(), capsFor(cmd), db, destination(db), r)
		},
	}
	cmd.Flags().StringVar(&flagSince, "since", "30d", "window to report on: 7d, 30d, 90d, or a date (YYYY-MM-DD)")
	cmd.Flags().BoolVar(&flagDetail, "detail", false, "show every row, with no truncation")
	cmd.Flags().StringVar(&flagVendor, "vendor", "", "report on one vendor only")
	cmd.Flags().StringVar(&flagBy, "by", "",
		"break spend down by one dimension: team, "+strings.Join(store.GroupByNames(), ", "))
	return cmd
}

// renderUsage prints the headline number and the footer.
func renderUsage(w io.Writer, caps ui.Caps, db *store.DB, dest sink.Sink, r timerange.Range) error {
	filter := store.Filter{From: r.FromDay(), To: r.ToDay(), Vendor: flagVendor}

	t, err := db.Totals(filter)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\n  AI SPEND %s %s %s %s\n\n",
		caps.Sep(), r.Label, caps.Sep(), rangeText(r, caps))

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

	// A change is never reported without naming what it is a change from.
	if prior, err := db.Totals(store.Filter{
		From: r.PriorFrom(), To: r.PriorTo(), Vendor: flagVendor,
	}); err == nil && prior.Priced > 0 && t.Priced > 0 {
		if text, noise := fmtutil.Delta(t.Micros, prior.Micros, caps.UTF8); text != "" {
			line := fmt.Sprintf("%s vs prior %d days", text, r.Days())
			if noise {
				// Under five percent is noise, not signal, and is rendered as
				// such rather than given the same weight as a real move.
				line = caps.Dim(line)
			}
			fmt.Fprintf(w, "  %-40s %14s\n", "", line)
		}
	}

	if t.Priced < t.Facts {
		fmt.Fprintf(w, "  %s\n", caps.Dim(fmt.Sprintf(
			"%d of %d facts carry no cost yet %s tokens are collected, pricing is not wired up",
			t.Facts-t.Priced, t.Facts, caps.Sep())))
	}

	views := []struct {
		by    store.GroupBy
		title string
	}{
		{store.ByVendor, "BY VENDOR"},
		{store.ByModel, "BY MODEL"},
	}
	if flagBy != "" {
		views = []struct {
			by    store.GroupBy
			title string
		}{{store.GroupBy(flagBy), "BY " + strings.ToUpper(flagBy)}}
	}

	if flagBy == "team" {
		if err := writeTeams(w, caps, db, filter, t); err != nil {
			return err
		}
	} else {
		for _, v := range views {
			if err := writeGroup(w, caps, db, v.by, v.title, filter, t); err != nil {
				return err
			}
		}
	}

	if err := writeAttribution(w, caps, db, filter, t); err != nil {
		return err
	}

	if err := writeSurprises(w, caps, db, filter, r, t); err != nil {
		return err
	}

	fmt.Fprintln(w)
	if err := writeFooter(w, caps, db, filter, dest, r, t); err != nil {
		return err
	}

	// The three commands that continue the conversation. Cheap, and it is what
	// turns a printed number into the next five minutes of a demo.
	fmt.Fprintf(w, "\n  %-9s %s\n", "Next",
		caps.Dim("aispend usage --by team    aispend export --share    aispend purge"))
	return nil
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

	rows := make([][4]string, 0, len(shown)+2)
	for _, g := range shown {
		rows = append(rows, [4]string{
			labelFor(by, g.Key, caps),
			fmtutil.MoneyOrUnknown(g.Micros, g.Priced > 0),
			sharePct(g.Micros, t.Micros, g.Priced > 0 && t.Micros > 0),
			trend(db, by, g.Key, filter, caps),
		})
	}

	if len(rest) > 0 {
		var micros int64
		var priced int
		for _, g := range rest {
			micros += g.Micros
			priced += g.Priced
		}
		rows = append(rows, [4]string{
			caps.Dim(fmt.Sprintf("…and %d more", len(rest))),
			fmtutil.MoneyOrUnknown(micros, priced > 0),
			sharePct(micros, t.Micros, priced > 0 && t.Micros > 0),
			"",
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
		fmt.Fprintf(w, "  %s%s  %12s  %7s   %s\n",
			row[0], strings.Repeat(" ", pad), row[1], row[2], caps.Dim(row[3]))
	}

	// The totals row is what makes the column checkable by eye rather than
	// taken on trust.
	fmt.Fprintf(w, "  %s\n", caps.Dim(strings.Repeat(lineRune(caps), width+23)))
	fmt.Fprintf(w, "  %s  %12s\n", strings.Repeat(" ", width),
		fmtutil.MoneyOrUnknown(t.Micros, t.Priced > 0))
	return nil
}

// trend renders a per-row sparkline over the last seven days of the window.
//
// Only for vendor rows: a sparkline per model or per key would be a wall of
// noise, and the vendor is the level at which a change is actionable.
func trend(db *store.DB, by store.GroupBy, key string, filter store.Filter, caps ui.Caps) string {
	if by != store.ByVendor {
		return ""
	}

	end, err := time.Parse("2006-01-02", filter.To)
	if err != nil {
		return ""
	}
	start := end.AddDate(0, 0, -6)
	if start.Format("2006-01-02") < filter.From {
		start, _ = time.Parse("2006-01-02", filter.From)
	}

	daily, err := db.DailyTotals(store.Filter{
		From: start.Format("2006-01-02"), To: filter.To, Vendor: key,
	})
	if err != nil {
		return ""
	}

	// A dense series: days with no data are zeros, not gaps. A gap silently
	// redrawn as a shorter line would misreport the shape.
	var values []int64
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		values = append(values, daily[d.Format("2006-01-02")])
	}
	return fmtutil.Sparkline(values, caps.UTF8)
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
	if by == store.ByKey || by == store.ByProject {
		// Vendor identifiers are opaque and long. The tail is what
		// distinguishes two of them at a glance, so that is what is kept.
		return shortRef(key)
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
func writeFooter(w io.Writer, caps ui.Caps, db *store.DB, filter store.Filter,
	dest sink.Sink, r timerange.Range, t store.Totals) error {
	line := strings.Repeat("─", 66)
	if !caps.UTF8 {
		line = strings.Repeat("-", 66)
	}
	fmt.Fprintf(w, "  %s\n", caps.Dim(line))

	if basis, err := basisLine(db, filter, caps); err != nil {
		return err
	} else if basis != "" {
		fmt.Fprintf(w, "  %-9s %s\n", "Basis", caps.Dim(basis))
	}

	fmt.Fprintf(w, "  %-9s %s\n", "Privacy", caps.Dim(privacyLine(dest)))
	fmt.Fprintf(w, "  %-9s %s\n", "Days", caps.Dim(daysLine(r, t)))
	return nil
}

// writeSurprises computes the things worth looking at, rather than hoping the
// reader spots them.
//
// The whole validation exercise turns on whether the number surprises someone,
// so the surprises are computed. When nothing is unusual the block is absent
// entirely — not an empty heading, which would read as a tool that found
// nothing to say.
func writeSurprises(w io.Writer, caps ui.Caps, db *store.DB, filter store.Filter,
	r timerange.Range, t store.Totals) error {

	if t.Facts == 0 || t.Priced == 0 {
		return nil
	}

	in, err := gatherSurpriseInput(db, filter, r, t)
	if err != nil {
		return err
	}

	found := analytics.Top(in, 3)
	if len(found) == 0 {
		return nil
	}

	fmt.Fprintf(w, "\n  %s  %d %s worth a look\n", caps.Warn(), len(found),
		plural(len(found), "thing", "things"))
	for _, s := range found {
		fmt.Fprintf(w, "     %s %s\n", bullet(caps), s.Text)
	}
	return nil
}

func bullet(caps ui.Caps) string {
	if caps.UTF8 {
		return "·"
	}
	return "-"
}

// gatherSurpriseInput reads everything the rules need in one place, so each rule
// stays a threshold and a sentence.
func gatherSurpriseInput(db *store.DB, filter store.Filter, r timerange.Range,
	t store.Totals) (analytics.Input, error) {

	in := analytics.Input{Window: r, Totals: t}

	var err error
	if in.Vendors, err = db.GroupBy(store.ByVendor, filter); err != nil {
		return in, err
	}
	if in.Models, err = db.GroupBy(store.ByModel, filter); err != nil {
		return in, err
	}
	if in.Keys, err = db.GroupBy(store.ByKey, filter); err != nil {
		return in, err
	}

	if prior, err := db.Totals(store.Filter{
		From: r.PriorFrom(), To: r.PriorTo(), Vendor: filter.Vendor,
	}); err == nil {
		in.PriorTotal = prior.Micros
	}

	// Split the window in half, so a trend can be detected inside whatever
	// range the reader asked for rather than only against a prior window that
	// may hold no data.
	mid := r.From.AddDate(0, 0, r.Days()/2)
	in.EarlierVendors, err = vendorTotals(db, store.Filter{
		From: r.FromDay(), To: mid.AddDate(0, 0, -1).Format("2006-01-02"), Vendor: filter.Vendor})
	if err != nil {
		return in, err
	}
	in.RecentVendors, err = vendorTotals(db, store.Filter{
		From: mid.Format("2006-01-02"), To: r.ToDay(), Vendor: filter.Vendor})
	if err != nil {
		return in, err
	}

	m, err := loadOwners()
	if err != nil {
		return in, err
	}
	for _, k := range in.Keys {
		if m.Team(k.Vendor, k.Key) == owners.Unattributed {
			in.Unattributed += k.Micros
			in.UnattributedKeys++
		}
	}
	return in, nil
}

func vendorTotals(db *store.DB, filter store.Filter) (map[string]int64, error) {
	groups, err := db.GroupBy(store.ByVendor, filter)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, g := range groups {
		out[g.Key] = g.Micros
	}
	return out, nil
}

// writeAttribution is the block that demonstrates the product thesis without a
// word of pitch.
//
// With no owners.csv, everything lands in Unattributed and it is displayed
// loudly. A reader seeing "Unattributed $12,024 (78%) across 31 keys" has just
// understood what the product is for.
func writeAttribution(w io.Writer, caps ui.Caps, db *store.DB, filter store.Filter, t store.Totals) error {
	if t.Facts == 0 {
		return nil
	}

	m, err := loadOwners()
	if err != nil {
		return err
	}

	keys, err := db.GroupBy(store.ByKey, filter)
	if err != nil {
		return err
	}

	var mapped, unmapped int64
	var mappedKeys, unmappedKeys int
	for _, g := range keys {
		vendor, principal := splitKeyRow(g)
		if m.Team(vendor, principal) == owners.Unattributed {
			unmapped += g.Micros
			unmappedKeys++
			continue
		}
		mapped += g.Micros
		mappedKeys++
	}

	fmt.Fprintf(w, "\n  ATTRIBUTION\n")
	fmt.Fprintf(w, "  %-34s  %12s  %7s\n", "Mapped to a team",
		fmtutil.MoneyOrUnknown(mapped, t.Priced > 0), sharePct(mapped, t.Micros, t.Micros > 0))

	label := fmt.Sprintf("%-34s", owners.Unattributed)
	detail := fmt.Sprintf("%d %s", unmappedKeys, plural(unmappedKeys, "key", "keys"))
	fmt.Fprintf(w, "  %s  %12s  %7s   %s\n", label,
		fmtutil.MoneyOrUnknown(unmapped, t.Priced > 0),
		sharePct(unmapped, t.Micros, t.Micros > 0), caps.Dim(detail))

	if !m.Loaded {
		fmt.Fprintf(w, "  %s\n", caps.Dim(
			"no owners.csv yet — drop one in "+config.Display(ownersPath())+" to split this by team"))
	}
	for _, warning := range m.Warnings {
		fmt.Fprintf(w, "  %s %s\n", caps.Warn(), warning)
	}
	return nil
}

// writeTeams renders --by team, which needs the owners map rather than a
// database column.
func writeTeams(w io.Writer, caps ui.Caps, db *store.DB, filter store.Filter, t store.Totals) error {
	m, err := loadOwners()
	if err != nil {
		return err
	}

	keys, err := db.GroupBy(store.ByKey, filter)
	if err != nil {
		return err
	}

	byTeam := map[string]*store.Group{}
	for _, g := range keys {
		vendor, principal := splitKeyRow(g)
		team := m.Team(vendor, principal)
		if byTeam[team] == nil {
			byTeam[team] = &store.Group{Key: team}
		}
		byTeam[team].Micros += g.Micros
		byTeam[team].Facts += g.Facts
		byTeam[team].Priced += g.Priced
	}

	rows := make([]store.Group, 0, len(byTeam))
	for _, g := range byTeam {
		rows = append(rows, *g)
	}
	// Descending by spend, but Unattributed last regardless of size: it is a
	// gap in the data, not a team, and sorting it into the middle of a list of
	// teams reads as though it were one.
	sort.Slice(rows, func(i, j int) bool {
		if (rows[i].Key == owners.Unattributed) != (rows[j].Key == owners.Unattributed) {
			return rows[j].Key == owners.Unattributed
		}
		return rows[i].Micros > rows[j].Micros
	})

	fmt.Fprintf(w, "\n  BY TEAM\n")
	for _, g := range rows {
		fmt.Fprintf(w, "  %-34s  %12s  %7s\n", g.Key,
			fmtutil.MoneyOrUnknown(g.Micros, g.Priced > 0),
			sharePct(g.Micros, t.Micros, t.Micros > 0))
	}
	fmt.Fprintf(w, "  %s\n", caps.Dim(strings.Repeat(lineRune(caps), 57)))
	fmt.Fprintf(w, "  %-34s  %12s\n", "", fmtutil.MoneyOrUnknown(t.Micros, t.Priced > 0))
	return nil
}

// splitKeyRow recovers the vendor for a key row. The key grouping collapses
// vendors, so the vendor is looked up from the underlying facts.
func splitKeyRow(g store.Group) (vendor, principal string) {
	return g.Vendor, g.Key
}

func ownersPath() string {
	paths, err := config.Resolve()
	if err != nil {
		return "~/.aispend/owners.csv"
	}
	return paths.Owners
}

func loadOwners() (*owners.Map, error) {
	return owners.Load(ownersPath())
}

// rangeText renders the date range for terminals that cannot draw an en dash.
// A header full of mojibake is a poor first impression in the one place a
// stranger decides whether to keep reading.
func rangeText(r timerange.Range, caps ui.Caps) string {
	if caps.UTF8 {
		return r.String()
	}
	return strings.ReplaceAll(r.String(), "–", "-")
}

// validateFlags checks the display flags before any output is produced.
func validateFlags() error {
	if flagBy == "team" {
		return nil // resolved from owners.csv rather than a database column
	}
	if flagBy != "" && !store.GroupBy(flagBy).Valid() {
		return fmt.Errorf("unknown --by %q\n\n  Valid values: %s",
			flagBy, strings.Join(store.GroupByNames(), ", "))
	}
	if flagVendor != "" {
		if _, ok := catalog.Get(flagVendor); !ok {
			var names []string
			for _, v := range catalog.Vendors() {
				names = append(names, v.ID)
			}
			return fmt.Errorf("unknown --vendor %q\n\n  Valid values: %s",
				flagVendor, strings.Join(names, ", "))
		}
	}
	return nil
}

// basisLine says where the money figure came from.
//
// This single line is the difference between a report a finance person forwards
// and one they quietly discard: "the vendor told us this" and "we worked this
// out from a price list" are different claims, and blurring them costs the
// reader's trust the first time they check.
func basisLine(db *store.DB, filter store.Filter, caps ui.Caps) (string, error) {
	splits, err := db.BasisBreakdown(filter)
	if err != nil {
		return "", err
	}

	label := map[string]string{
		"vendor_reported": "vendor-reported",
		"allocated":       "allocated to model",
		"computed":        "computed from the price book",
		"unknown":         "not priced",
	}

	var parts []string
	for _, s := range splits {
		if s.Basis == "unknown" {
			parts = append(parts, fmt.Sprintf("%d %s %s",
				s.Facts, plural(s.Facts, "fact", "facts"), label[s.Basis]))
			continue
		}
		name := label[s.Basis]
		if name == "" {
			name = s.Basis
		}
		parts = append(parts, fmtutil.Money(s.Micros)+" "+name)
	}
	if len(parts) == 0 {
		return "", nil
	}

	line := strings.Join(parts, " "+caps.Sep()+" ")

	if versions, err := db.PriceVersions(filter); err == nil && len(versions) > 0 {
		line += " (price book " + strings.Join(versions, ", ") + ")"
	}

	// Naming what could not be priced is the other half of the claim: a total
	// that silently omits a model is worse than one that says what it missed.
	if unpriced, err := db.UnpricedModels(filter); err == nil && len(unpriced) > 0 {
		line += "\n            " + caps.Warn() + " no price for " + strings.Join(unpriced, ", ")
	}
	return line, nil
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
