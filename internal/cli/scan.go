package cli

import (
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
	"github.com/prabhuvmk/aispend/internal/egress"
	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/fmtutil"
	"github.com/prabhuvmk/aispend/internal/timerange"
	"github.com/prabhuvmk/aispend/internal/ui"
)

var (
	flagSince   string
	flagDryRun  bool
	flagKeepRaw bool
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
			return runScan(cmd, out, caps, r)
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

// runScan collects from every connected vendor and prints what it found.
//
// Nothing is written to the database in this build: the facts are printed so the
// mapping from a vendor's response onto the fact schema can be checked against
// the vendor's own dashboard before anything is stored. Persistence is the next
// run.
func runScan(cmd *cobra.Command, w io.Writer, caps ui.Caps, r timerange.Range) error {
	client, err := scanClient()
	if err != nil {
		return err
	}
	registry := collect.New(client)

	var (
		total    int
		problems []*collect.VendorError
		unpriced int
	)

	fmt.Fprintf(w, "\n  Scanning %s\n\n", r.String())

	for _, v := range catalog.Vendors() {
		c := cred.Resolve(v)
		if c.Empty() {
			continue
		}
		collector, ok := registry.Get(v.ID)
		if !ok {
			fmt.Fprintf(w, "  %s %-11s %s\n", caps.Dash(), v.ID, "not implemented in this build")
			continue
		}

		started := time.Now()
		count := 0
		_, err := collector.Collect(cmd.Context(), c, r, "", func(f fact.Fact) error {
			count++
			if f.AmountBasis == fact.BasisUnknown {
				unpriced++
			}
			fmt.Fprintln(w, "  "+factLine(f, caps))
			return nil
		})
		total += count

		switch {
		case errors.Is(err, collect.ErrNotImplemented):
			// Not built yet is not a failure. It prints as absent, like a
			// vendor with no credential, rather than as something gone wrong.
			fmt.Fprintf(w, "  %s %-11s %s\n", caps.Dash(), v.ID,
				caps.Dim("collector not implemented in this build"))
		case err != nil:
			var ve *collect.VendorError
			if errors.As(err, &ve) {
				problems = append(problems, ve)
				fmt.Fprintf(w, "  %s %-11s %s\n", caps.Fail(), v.ID, ve.What)
			} else {
				fmt.Fprintf(w, "  %s %-11s %s\n", caps.Fail(), v.ID, err.Error())
			}
		default:
			fmt.Fprintf(w, "  %s %-11s %s facts %s %s\n", caps.OK(), v.ID,
				fmtutil.Tokens(int64(count)), caps.Sep(),
				time.Since(started).Round(time.Millisecond/10).String())
		}
	}

	if total == 0 && len(problems) == 0 {
		// No usage is a finding, not an empty screen. An empty report would
		// leave the reader unsure whether the tool worked.
		fmt.Fprintf(w, "  %s\n",
			caps.Dim("no usage reported in this window — try a wider one, e.g. --since 90d"))
		return nil
	}

	if unpriced > 0 {
		fmt.Fprintf(w, "\n  %s\n", caps.Dim(fmt.Sprintf(
			"%d facts have no cost attached yet %s the price book and the vendor cost report are not wired up",
			unpriced, caps.Sep())))
	}
	fmt.Fprintf(w, "\n  %s\n", caps.Dim("nothing was written to the database in this build"))

	for _, ve := range problems {
		fmt.Fprintf(w, "\n  %s %s\n  %s\n", ve.Vendor, ve.What, wrapIndent(ve.Why, 74, "  "))
		if ve.Fix != "" {
			fmt.Fprintf(w, "\n  Fix:  %s\n", ve.Fix)
		}
	}
	return nil
}

// factLine is the one-line rendering used while the collectors are being built,
// so the mapping can be eyeballed against a vendor dashboard.
func factLine(f fact.Fact, caps ui.Caps) string {
	parts := []string{
		"fact ", f.Vendor, f.Day,
		orDash(f.WorkspaceRef, caps), orDash(f.PrincipalRef, caps), orDash(f.ModelRef, caps),
		"in=" + fmtutil.Tokens(f.InputUnits),
		"out=" + fmtutil.Tokens(f.OutputUnits),
	}
	if f.CachedUnits > 0 {
		parts = append(parts, "cached="+fmtutil.Tokens(f.CachedUnits))
	}
	if f.OtherUnits > 0 {
		parts = append(parts, "other="+fmtutil.Tokens(f.OtherUnits))
	}
	parts = append(parts,
		fmtutil.MoneyOrUnknown(f.AmountMicros, f.AmountBasis != fact.BasisUnknown),
		string(f.AmountBasis))
	return strings.Join(parts, "  ")
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
