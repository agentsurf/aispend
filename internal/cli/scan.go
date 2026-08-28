package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/catalog"
	"github.com/prabhuvmk/aispend/internal/collect"
	"github.com/prabhuvmk/aispend/internal/cred"
	"github.com/prabhuvmk/aispend/internal/dbg"
	"github.com/prabhuvmk/aispend/internal/egress"
	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/fmtutil"
	"github.com/prabhuvmk/aispend/internal/sink"
	"github.com/prabhuvmk/aispend/internal/store"
	"github.com/prabhuvmk/aispend/internal/timerange"
	"github.com/prabhuvmk/aispend/internal/ui"
)

var (
	flagSince   string
	flagDryRun  bool
	flagKeepRaw bool
	flagDetail  bool
	flagVendor  string
	flagBy      string
)

func newScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Collect usage from every connected vendor, then report",
		Long: `scan detects credentials, collects usage, and prints a report.

It contacts vendor APIs and nothing else: the list of hosts it is permitted to
reach is compiled into the binary, enforced in the network dialer, and printed
in full by --dry-run.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := timerange.Parse(flagSince, time.Now())
			if err != nil {
				return err
			}

			out, caps := cmd.OutOrStdout(), capsFor(cmd)
			if flagDryRun {
				return dryRun(out, caps, r)
			}
			return runScan(cmd, out, caps, r, false)
		},
	}

	cmd.Flags().StringVar(&flagSince, "since", "30d", "window to collect: 7d, 30d, 90d, or a date (YYYY-MM-DD)")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "print every request that would be made, then exit without making any")
	cmd.Flags().BoolVar(&flagKeepRaw, "keep-raw", false, "save each vendor response under ~/.aispend/raw/ so a figure can be traced to its source")
	return cmd
}

// httpClient returns the client for this invocation: the egress-guarded one, or
// a fixture transport that opens no sockets at all.
func httpClient() *http.Client {
	if flagFixture != "" {
		return egress.NewFixture(flagFixture)
	}
	return egress.New(catalog.IsAllowedHost)
}

// plannedRequest is one line of the dry run.
type plannedRequest struct {
	Vendor    string
	Method    string
	Host      string
	Path      string
	Count     int
	Connected bool
}

// planRequests works out what a scan would ask for. It is derived from the
// catalog and the window, never hand-written, so --dry-run cannot drift away
// from what the binary actually does.
func planRequests(r timerange.Range) []plannedRequest {
	var out []plannedRequest
	for _, v := range catalog.Vendors() {
		c := cred.Resolve(v)
		host := v.AllowedHosts[0]

		for _, name := range []string{"verify", "usage", "costs"} {
			path, ok := v.Endpoints[name]
			if !ok || path == "" {
				continue
			}
			count := 1
			if name != "verify" {
				// One request per day is the upper bound. Vendors page their
				// usage reports, so the real number is usually lower.
				count = r.Days()
			}
			out = append(out, plannedRequest{
				Vendor: v.ID, Method: http.MethodGet, Host: host,
				Path: path, Count: count, Connected: !c.Empty(),
			})
		}
	}
	return out
}

func dryRun(w io.Writer, caps ui.Caps, r timerange.Range) error {
	plan := planRequests(r)

	fmt.Fprintf(w, "\n  DRY RUN %s %s %s no request is made, nothing is written\n\n",
		caps.Sep(), r.String(), caps.Sep())

	connected := 0
	for _, p := range plan {
		if !p.Connected {
			continue
		}
		connected++
		fmt.Fprintf(w, "  would %s %s%s ×%d\n", p.Method, p.Host, p.Path, p.Count)
	}
	if connected == 0 {
		fmt.Fprintf(w, "  %s\n", caps.Dim("no vendor is connected, so no request would be made"))
	}

	skipped := map[string]bool{}
	for _, p := range plan {
		if !p.Connected {
			skipped[p.Vendor] = true
		}
	}
	if len(skipped) > 0 {
		var names []string
		for _, v := range catalog.Vendors() {
			if skipped[v.ID] {
				names = append(names, v.ID)
			}
		}
		fmt.Fprintf(w, "\n  %s\n", caps.Dim("skipped, no credential: "+strings.Join(names, ", ")))
	}

	// Generated from the allowlist the dialer enforces, never written by hand,
	// so this statement is verifiable rather than promotional.
	fmt.Fprintf(w, "\n  %s\n", caps.Dim(
		"aispend is structurally incapable of contacting any other host. Permitted: "+
			strings.Join(catalog.AllowedHosts(), ", ")))
	return nil
}

// verifyVendors runs Verify against every connected vendor, for doctor.
func verifyVendors(cmd *cobra.Command) []vendorCheck {
	registry := collect.New(httpClient())
	var out []vendorCheck

	for _, v := range catalog.Vendors() {
		c := cred.Resolve(v)
		check := vendorCheck{Vendor: v.ID}

		switch collector, ok := registry.Get(v.ID); {
		case c.Empty():
			// No credential is not a failure. It is a vendor this machine has
			// not been told about, and it prints as absent.
			check.NoCredential = true
		case !ok:
			check.Unsupported = true
		default:
			started := time.Now()
			info, err := collector.Verify(cmd.Context(), c)
			check.Took = time.Since(started)
			check.Info, check.Err = info, err
		}
		out = append(out, check)
	}
	return out
}

type vendorCheck struct {
	Vendor       string
	NoCredential bool
	Unsupported  bool
	Info         collect.AccountInfo
	Err          error
	Took         time.Duration
}

// runScan collects from every connected vendor, stores what it finds, and
// reports what happened.
//
// Vendors run concurrently and independently: one vendor failing must never
// abort the others, because a report covering two of three with a clear
// explanation of the third is worth far more than no report at all.
func runScan(cmd *cobra.Command, w io.Writer, caps ui.Caps, r timerange.Range, quiet bool) error {
	client, err := scanClient()
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

	dest := destination(db)
	registry := collect.New(client)
	ctx := cmd.Context()

	var (
		jobs     []collect.Job
		emitters = map[string]*storeEmitter{}
	)

	for _, v := range catalog.Vendors() {
		c := cred.Resolve(v)
		if c.Empty() {
			continue
		}
		collector, ok := registry.Get(v.ID)
		if !ok {
			// A vendor in the catalog with a credential set but no collector
			// yet must say so. Skipping it silently would let a connected
			// OpenRouter vanish from the report with no explanation, which
			// reads as "aispend found no OpenRouter spend".
			vendorID := v.ID
			jobs = append(jobs, collect.Job{
				Vendor: vendorID,
				Count:  func() int { return 0 },
				Run: func(context.Context) (string, error) {
					return "", fmt.Errorf("%s: %w", vendorID, collect.ErrNotImplemented)
				},
			})
			continue
		}

		// Resume from wherever the last run stopped. A cursor from a different
		// window would point into the wrong result set, so it is only reused
		// when the window still covers what it was recorded against.
		state, err := db.SyncState(v.ID)
		if err != nil {
			return err
		}
		cursor := ""
		if state.Cursor != "" && state.CoveredFrom == r.FromDay() && state.CoveredTo == r.ToDay() {
			cursor = state.Cursor
			dbg.Printf("resuming %s from a stored cursor", v.ID)
			if !quiet {
				fmt.Fprintf(w, "  %s\n", caps.Dim("resuming "+v.ID+" from "+state.CoveredTo))
			}
		}

		em := newStoreEmitter(ctx, db, dest, v.ID, r)
		if !quiet {
			em.printer = func(f fact.Fact) { fmt.Fprintln(w, "  "+factLine(f, caps)) }
		}
		emitters[v.ID] = em

		vendorID, collectorRef, credRef, cur := v.ID, collector, c, cursor
		jobs = append(jobs, collect.Job{
			Vendor: vendorID,
			Count:  em.Count,
			Run: func(ctx context.Context) (string, error) {
				next, err := collectorRef.Collect(ctx, credRef, r, cur, em)
				// Flush regardless: a failure part-way through must keep the
				// facts already read, not discard them.
				if flushErr := em.Close(); err == nil {
					err = flushErr
				}
				return next, err
			},
		})
	}

	if len(jobs) == 0 {
		fmt.Fprintf(w, "\n  %s\n", caps.Dim(
			"no vendor is connected — set a credential, or run: aispend connections"))
		return nil
	}

	if !quiet {
		fmt.Fprintf(w, "\n  Scanning %d %s %s\n\n", len(jobs),
			plural(len(jobs), "connection", "connections"), r.String())
	}

	results := collect.Run(ctx, jobs, 4)
	return reportScan(cmd, w, caps, db, dest, r, results, quiet)
}

// reportScan prints the per-vendor outcome and records it in sync_state.
func reportScan(cmd *cobra.Command, w io.Writer, caps ui.Caps, db *store.DB, dest sink.Sink,
	r timerange.Range, results []collect.Result, quiet bool) error {

	var (
		total    int
		problems []*collect.VendorError
	)

	for _, res := range results {
		// Whatever happened, record it: last_error is how the next run explains
		// a vendor that has been failing quietly.
		state := store.SyncState{
			Vendor: res.Vendor, CoveredFrom: r.FromDay(), CoveredTo: r.ToDay(),
			Cursor: res.Cursor, LastRunAt: time.Now().UTC().Unix(),
		}

		switch {
		case errors.Is(res.Err, collect.ErrNotImplemented):
			fmt.Fprintf(w, "  %s %-11s %s\n", caps.Dash(), res.Vendor,
				caps.Dim("collector not implemented in this build"))
			continue
		case res.Err != nil:
			state.LastError = res.Err.Error()
			var ve *collect.VendorError
			if errors.As(res.Err, &ve) {
				problems = append(problems, ve)
				fmt.Fprintf(w, "  %s %-11s %s\n", caps.Fail(), res.Vendor, ve.What)
			} else {
				fmt.Fprintf(w, "  %s %-11s %s\n", caps.Fail(), res.Vendor, res.Err.Error())
			}
		default:
			total += res.Facts
			fmt.Fprintf(w, "  %s %-11s %d facts %s %s\n", caps.OK(), res.Vendor,
				res.Facts, caps.Sep(), res.Took.Round(time.Millisecond/10))
		}

		if err := db.SaveSyncState(state); err != nil {
			return err
		}
	}

	if total == 0 && len(problems) == 0 {
		fmt.Fprintf(w, "\n  %s\n", caps.Dim(
			"no usage reported in this window — try a wider one, e.g. --since 90d"))
	}

	for _, ve := range problems {
		fmt.Fprintf(w, "\n  %s %s\n  %s\n", ve.Vendor, ve.What, wrapIndent(ve.Why, 74, "  "))
		if ve.Fix != "" {
			fmt.Fprintf(w, "\n  Fix:  %s\n", ve.Fix)
		}
	}

	if quiet {
		return nil
	}
	if err := renderUsage(w, caps, db, dest, r); err != nil {
		return err
	}

	// One optional question at the end of a scan. It is skippable with Enter
	// and silently absent without a terminal, and it is the metric this whole
	// exercise exists to measure.
	if surprised := askSurprised(cmd); surprised != "" {
		fmt.Fprintf(w, "  %s\n", caps.Dim(
			"noted — include it with: aispend export --share"))
	}
	return nil
}

// destination builds the sink chain for this run. There is exactly one sink in
// v1, and the Privacy footer is generated from whatever this returns.
func destination(db *store.DB) sink.Sink {
	return sink.NewSQLite(db.SQL())
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// factLine is the one-line rendering used while the collectors are being built,
// so the mapping can be eyeballed against a vendor dashboard.
func factLine(f fact.Fact, caps ui.Caps) string {
	parts := []string{
		"fact ", f.Vendor, f.Day,
		orDash(shortRef(f.WorkspaceRef), caps), orDash(shortRef(f.PrincipalRef), caps),
		orDash(f.ModelRef, caps),
		"in=" + fmtutil.Tokens(f.InputUnits),
		"out=" + fmtutil.Tokens(f.OutputUnits),
	}
	if f.CachedUnits > 0 {
		parts = append(parts, "cacheread="+fmtutil.Tokens(f.CachedUnits))
	}
	if f.CacheWriteUnits > 0 {
		parts = append(parts, "cachewrite="+fmtutil.Tokens(f.CacheWriteUnits))
	}
	if f.OtherUnits > 0 {
		parts = append(parts, "other="+fmtutil.Tokens(f.OtherUnits))
	}
	parts = append(parts,
		fmtutil.MoneyOrUnknown(f.AmountMicros, f.AmountBasis != fact.BasisUnknown),
		string(f.AmountBasis))
	return strings.Join(parts, "  ")
}

// shortRef trims a vendor identifier to something a terminal line can hold.
// These are opaque ids, not secrets, so the tail is kept — it is what
// distinguishes two keys from each other at a glance.
func shortRef(s string) string {
	const max = 16
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:6]) + "…" + string(r[len(r)-6:])
}

func orDash(s string, caps ui.Caps) string {
	if s == "" {
		return caps.Dash()
	}
	return s
}

// scanClient builds the HTTP client for this invocation: fixtures, or the
// egress-guarded one, optionally wrapped to keep raw responses.
func scanClient() (*http.Client, error) {
	client := httpClient()
	if !flagKeepRaw {
		return client, nil
	}
	paths, err := resolvePaths()
	if err != nil {
		return nil, err
	}
	return egress.KeepRaw(client, paths.Raw), nil
}
