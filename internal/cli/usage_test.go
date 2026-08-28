package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prabhuvmk/aispend/internal/egress"
	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/sink"
	"github.com/prabhuvmk/aispend/internal/store"
	"github.com/prabhuvmk/aispend/internal/timerange"
	"github.com/prabhuvmk/aispend/internal/ui"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "aispend.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testWindow(t *testing.T) timerange.Range {
	t.Helper()
	r, err := timerange.Parse("30d", time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// remoteSink stands in for the control plane the sidecar posture will push to.
type remoteSink struct{}

func (remoteSink) Write(context.Context, []fact.Fact) error { return nil }
func (remoteSink) Flush(context.Context) error              { return nil }
func (remoteSink) Describe() string                         { return "the aispend control plane" }

// Design §9.4: "scan and sync keep the Privacy footer, and it stays true — if a
// sink other than SQLite is configured, the footer must say so instead. Make
// that a test, not a convention."
//
// This is that test. It is written now, while there is one sink and the claim
// is trivially true, because the run where it stops being trivially true is the
// run where it would be forgotten.
func TestPrivacyFooterIsDerivedFromTheSinks(t *testing.T) {
	egress.ResetContacted()
	local := sink.NewSQLite(testDB(t).SQL())

	got := privacyLine(local)
	if !strings.Contains(got, "Nothing left this machine") {
		t.Errorf("local-only sink does not claim locality: %q", got)
	}

	// Adding a second, remote sink must change the sentence on its own — no
	// edit to this function, no flag, no remembering.
	got = privacyLine(sink.MultiSink{local, remoteSink{}})
	if strings.Contains(got, "Nothing left this machine") {
		t.Errorf("a configured remote sink still claimed nothing left the machine: %q", got)
	}
	if !strings.Contains(got, "control plane") {
		t.Errorf("the footer does not name the destination: %q", got)
	}
}

// An unrecognised sink is assumed to leave the machine. Guessing the other way
// would let a sink added later inherit the privacy claim by default.
func TestUnknownSinkDoesNotInheritThePrivacyClaim(t *testing.T) {
	if sink.Local(remoteSink{}) {
		t.Error("an unrecognised sink was treated as local")
	}
}

// The line reports what this run did, not what the binary is permitted to do.
func TestPrivacyLineReportsWhatWasActuallyContacted(t *testing.T) {
	egress.ResetContacted()
	local := sink.NewSQLite(testDB(t).SQL())

	if got := privacyLine(local); !strings.Contains(got, "No network was used") {
		t.Errorf("an offline run did not say so: %q", got)
	}

	// A blocked attempt is not a contact: nothing was reached.
	client := egress.New(egress.AllowOnly("api.openai.com"))
	if resp, err := client.Get("https://example.com/"); err == nil {
		resp.Body.Close()
	}
	if got := privacyLine(local); !strings.Contains(got, "No network was used") {
		t.Errorf("a blocked request was counted as a contact: %q", got)
	}
}

// Zero and "we could not determine it" are different facts. With nothing
// priced, the headline must be an em dash and a footnote.
func TestHeadlineIsUnknownNotZeroWhenNothingIsPriced(t *testing.T) {
	egress.ResetContacted()
	db := testDB(t)
	s := sink.NewSQLite(db.SQL())

	f := fact.Fact{
		Vendor: "openai", Day: "2026-08-27", ModelRef: "gpt-5.2", UnitKind: "token",
		InputUnits: 1000, AmountMicros: 0, AmountBasis: fact.BasisUnknown,
		Revision: 1, CollectedAt: time.Now().UTC(),
	}
	if err := s.Write(context.Background(), []fact.Fact{f}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var buf bytes.Buffer
	if err := renderUsage(&buf, ui.Caps{UTF8: true}, db, s, testWindow(t)); err != nil {
		t.Fatalf("renderUsage: %v", err)
	}
	got := buf.String()

	if strings.Contains(got, "$0") {
		t.Errorf("an undeterminable total was printed as zero:\n%s", got)
	}
	if !strings.Contains(got, "—") {
		t.Errorf("no em dash for the unknown total:\n%s", got)
	}
	if !strings.Contains(got, "carry no cost yet") {
		t.Errorf("the em dash has no footnote explaining it:\n%s", got)
	}
}

// A genuine zero must still print as a zero.
func TestPricedZeroStillPrintsAsZero(t *testing.T) {
	egress.ResetContacted()
	db := testDB(t)
	s := sink.NewSQLite(db.SQL())

	if err := s.Write(context.Background(), []fact.Fact{{
		Vendor: "openai", Day: "2026-08-27", ModelRef: "gpt-5.2", UnitKind: "token",
		AmountMicros: 0, AmountBasis: fact.BasisVendorReported,
		Revision: 1, CollectedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var buf bytes.Buffer
	renderUsage(&buf, ui.Caps{UTF8: true}, db, s, testWindow(t))
	if !strings.Contains(buf.String(), "$0.00") {
		t.Errorf("a vendor-reported zero was not shown as $0.00:\n%s", buf.String())
	}
}

func TestUsageOnAnEmptyDatabaseSaysSo(t *testing.T) {
	egress.ResetContacted()
	db := testDB(t)

	var buf bytes.Buffer
	if err := renderUsage(&buf, ui.Caps{UTF8: true}, db, sink.NewSQLite(db.SQL()), testWindow(t)); err != nil {
		t.Fatalf("renderUsage: %v", err)
	}
	if !strings.Contains(buf.String(), "aispend scan") {
		t.Errorf("an empty database does not say what to do:\n%s", buf.String())
	}
}

func TestFooterStatesTheTimezone(t *testing.T) {
	egress.ResetContacted()
	db := testDB(t)
	s := sink.NewSQLite(db.SQL())
	s.Write(context.Background(), []fact.Fact{{
		Vendor: "openai", Day: "2026-08-27", ModelRef: "m", UnitKind: "token",
		AmountBasis: fact.BasisUnknown, Revision: 1, CollectedAt: time.Now().UTC(),
	}})

	var buf bytes.Buffer
	renderUsage(&buf, ui.Caps{UTF8: true}, db, s, testWindow(t))
	if !strings.Contains(buf.String(), "All dates UTC") {
		t.Errorf("the footer does not state the timezone convention:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "restate") {
		t.Errorf("the footer does not warn that recent days may change:\n%s", buf.String())
	}
}
