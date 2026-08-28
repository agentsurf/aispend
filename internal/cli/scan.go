package cli

import (
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
	"github.com/prabhuvmk/aispend/internal/timerange"
	"github.com/prabhuvmk/aispend/internal/ui"
)

var (
	flagSince  string
	flagDryRun bool
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
			return fmt.Errorf(
				"collection is not wired up in this build yet\n\n" +
					"  Try:  aispend scan --dry-run    (shows exactly what it would request)\n" +
					"        aispend doctor            (checks each credential against its vendor)")
		},
	}

	cmd.Flags().StringVar(&flagSince, "since", "30d", "window to collect: 7d, 30d, 90d, or a date (YYYY-MM-DD)")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "print every request that would be made, then exit without making any")
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
