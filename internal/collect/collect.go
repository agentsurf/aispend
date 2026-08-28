// Package collect defines what a vendor connector is and holds the three
// implementations.
//
// One interface, one file per vendor, no plugin architecture: there are three
// vendors, not forty, and a registry that can be extended at runtime would buy
// nothing and cost a day.
package collect

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/prabhuvmk/aispend/internal/catalog"
	"github.com/prabhuvmk/aispend/internal/cred"
	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/timerange"
)

// Collector talks to one vendor.
type Collector interface {
	// Vendor is the catalog id.
	Vendor() string

	// Verify makes one cheap read call and reports what the credential could
	// actually see. It writes nothing, and it is what `doctor` runs.
	Verify(ctx context.Context, c cred.Credential) (AccountInfo, error)

	// Collect fetches facts for the window and streams them to the emitter.
	//
	// Streaming rather than returning a slice means a collector writes through
	// to the database as it goes: a partial failure keeps what it got, and a
	// 30-day backfill never holds 30 days of facts in memory.
	//
	// PageDone must be called after every page is fully emitted, so an
	// interrupted backfill loses at most one page rather than the run.
	Collect(ctx context.Context, c cred.Credential, r timerange.Range,
		cursor string, out Emitter) (nextCursor string, err error)
}

// Emitter receives what a collector produces.
//
// The two methods exist because the unit a collector emits (a fact) and the
// unit it can safely resume from (a page) are different. Persisting the cursor
// after every page is what turns Ctrl-C during a 30-day backfill from "start
// again" into "lose at most one page".
type Emitter interface {
	// Emit records one fact.
	Emit(f fact.Fact) error
	// PageDone reports that every fact up to cursor has been emitted.
	PageDone(cursor string) error
}

// EmitterFunc adapts a plain function, for callers that do not resume — the
// stdout printer and most tests.
type EmitterFunc func(fact.Fact) error

func (f EmitterFunc) Emit(x fact.Fact) error { return f(x) }
func (EmitterFunc) PageDone(string) error    { return nil }

// ErrNotImplemented means this vendor's collector has not been written yet.
//
// It is deliberately distinct from a vendor failure: "we have not built this"
// and "your credential was rejected" call for different reactions from the
// reader, and rendering the first as a red ✗ trains people to ignore the mark
// that matters. It disappears vendor by vendor as the collectors land.
var ErrNotImplemented = errors.New("collection is not implemented in this build yet")

// AccountInfo is what a credential could see: enough to prove the key works and
// to tell the user which account they are looking at, and nothing more.
type AccountInfo struct {
	AccountRef string   // org id as the vendor reports it
	Label      string   // human name, if the vendor gives one
	Details    []string // "3 projects", "28 keys" — rendered as-is
}

// Registry maps vendor id to collector.
type Registry map[string]Collector

// New builds the registry for this build. The HTTP client is passed in so the
// same collectors run against the live egress-guarded client, a fixture
// directory, or a test server, with no knowledge of which.
func New(client *http.Client) Registry {
	return Registry{
		"openai":    &openAI{http: client, limiter: limiterFor("openai")},
		"anthropic": &anthropic{http: client, limiter: limiterFor("anthropic")},
	}
}

// limiterFor builds a vendor's rate limiter from its catalog entry, so the
// limit and the vendor definition cannot drift apart.
func limiterFor(vendorID string) *limiter {
	v, ok := catalog.Get(vendorID)
	if !ok {
		return newLimiter(0)
	}
	return newLimiter(v.RateLimitRPS)
}

// Get returns the collector for a vendor.
func (r Registry) Get(vendor string) (Collector, bool) {
	c, ok := r[vendor]
	return c, ok
}

// Vendors lists the implemented vendors, in catalog order so output is stable.
func (r Registry) Vendors() []string {
	var out []string
	for _, v := range catalog.Vendors() {
		if _, ok := r[v.ID]; ok {
			out = append(out, v.ID)
		}
	}
	if len(out) == 0 {
		for id := range r {
			out = append(out, id)
		}
		sort.Strings(out)
	}
	return out
}

// endpoint builds a full URL from the catalog, so no collector hard-codes a
// host and the allowlist and the request can never disagree.
func endpoint(vendorID, name string) (string, error) {
	v, ok := catalog.Get(vendorID)
	if !ok {
		return "", fmt.Errorf("unknown vendor %q", vendorID)
	}
	path, ok := v.Endpoints[name]
	if !ok || path == "" {
		return "", fmt.Errorf("%s has no %s endpoint in the catalog", vendorID, name)
	}
	return "https://" + v.AllowedHosts[0] + path, nil
}
