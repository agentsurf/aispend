package cli

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"path/filepath"
	"strconv"
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

func seedExport(t *testing.T, db *store.DB) {
	t.Helper()
	s := sink.NewSQLite(db.SQL())
	at := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	err := s.Write(context.Background(), []fact.Fact{
		{Vendor: "anthropic", Day: "2026-08-27", WorkspaceRef: "wrkspc_1",
			PrincipalRef: "apikey_1", ModelRef: "claude-opus-4-6", UnitKind: "token",
			InputUnits: 811500, OutputUnits: 364000, CachedUnits: 2080000,
			CacheWriteUnits: 988000, OtherUnits: 40,
			AmountMicros: 20_372_500, AmountBasis: fact.BasisComputed,
			PriceVersion: "2026.08", Revision: 1, CollectedAt: at},
		{Vendor: "openrouter", Day: "2026-08-27", PrincipalRef: "sk-or-1",
			ModelRef: "google/gemini-3-pro", UnitKind: "token",
			InputUnits: 61000, OutputUnits: 7400,
			AmountMicros: 520_000, AmountBasis: fact.BasisVendorReported,
			Revision: 1, CollectedAt: at},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// Anything reading these formats is going to do arithmetic on them, so money is
// raw integer micros and token counts are exact. The humanised forms exist for
// a terminal only.
func TestJSONCarriesExactIntegers(t *testing.T) {
	db := testDB(t)
	seedExport(t, db)

	var buf bytes.Buffer
	filter := store.Filter{From: "2026-08-01", To: "2026-08-31"}
	if err := exportJSON(&buf, db, filter, testWindow(t)); err != nil {
		t.Fatalf("exportJSON: %v", err)
	}

	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if got := doc["total_micros"].(json.Number).String(); got != "20892500" {
		t.Errorf("total_micros = %s, want 20892500", got)
	}
	// No humanised strings, and no floats.
	body := buf.String()
	for _, bad := range []string{`"$`, "811K", "2.1M", `"20.37"`} {
		if strings.Contains(body, bad) {
			t.Errorf("JSON contains a humanised value %q", bad)
		}
	}
	facts := doc["facts"].([]any)
	first := facts[0].(map[string]any)
	for _, field := range []string{"input_units", "amount_micros", "cache_write_units"} {
		n, ok := first[field].(json.Number)
		if !ok {
			t.Errorf("%s is not a number", field)
			continue
		}
		if strings.ContainsAny(n.String(), ".eE") {
			t.Errorf("%s = %s is a float", field, n)
		}
	}
}

// A number a spreadsheet can add: no currency symbol, no thousands separator.
func TestCSVAmountsAreParseableNumbers(t *testing.T) {
	db := testDB(t)
	seedExport(t, db)

	var buf bytes.Buffer
	if err := exportCSV(&buf, db, store.Filter{From: "2026-08-01", To: "2026-08-31"}); err != nil {
		t.Fatalf("exportCSV: %v", err)
	}

	records, err := csv.NewReader(bytes.NewReader(buf.Bytes())).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("got %d rows (including header), want 3", len(records))
	}

	header := records[0]
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	for _, needed := range []string{"vendor", "day", "model", "amount_usd", "amount_micros", "amount_basis"} {
		if _, ok := col[needed]; !ok {
			t.Errorf("CSV has no %q column", needed)
		}
	}

	var sumMicros int64
	for _, rec := range records[1:] {
		if _, err := strconv.ParseFloat(rec[col["amount_usd"]], 64); err != nil {
			t.Errorf("amount_usd %q is not a plain number: %v", rec[col["amount_usd"]], err)
		}
		n, err := strconv.ParseInt(rec[col["amount_micros"]], 10, 64)
		if err != nil {
			t.Errorf("amount_micros %q is not an integer", rec[col["amount_micros"]])
		}
		sumMicros += n
	}
	if sumMicros != 20_892_500 {
		t.Errorf("CSV micros sum to %d, want 20892500", sumMicros)
	}
}

// The three formats must never disagree: a reader who checks one against
// another and finds a difference stops trusting all three.
func TestEveryFormatReportsTheSameTotal(t *testing.T) {
	db := testDB(t)
	seedExport(t, db)
	filter := store.Filter{From: "2026-08-01", To: "2026-08-31"}

	totals, err := db.Totals(filter)
	if err != nil {
		t.Fatal(err)
	}

	var jsonBuf bytes.Buffer
	exportJSON(&jsonBuf, db, filter, testWindow(t))
	var doc struct {
		Total int64 `json:"total_micros"`
	}
	json.Unmarshal(jsonBuf.Bytes(), &doc)

	var csvBuf bytes.Buffer
	exportCSV(&csvBuf, db, filter)
	records, _ := csv.NewReader(bytes.NewReader(csvBuf.Bytes())).ReadAll()
	var csvTotal int64
	for _, rec := range records[1:] {
		n, _ := strconv.ParseInt(rec[12], 10, 64)
		csvTotal += n
	}

	if doc.Total != totals.Micros || csvTotal != totals.Micros {
		t.Errorf("totals disagree: report=%d json=%d csv=%d", totals.Micros, doc.Total, csvTotal)
	}
}

// No output format may carry key material. The principal column holds the
// vendor's own key *identifier*, which is not a secret, and nothing else.
func TestExportsCarryNoCredentialMaterial(t *testing.T) {
	const secret = "sk-test-0000000000000000a4f2"
	t.Setenv("OPENAI_ADMIN_KEY", secret)

	db := testDB(t)
	seedExport(t, db)
	filter := store.Filter{From: "2026-08-01", To: "2026-08-31"}

	var jsonBuf, csvBuf bytes.Buffer
	exportJSON(&jsonBuf, db, filter, testWindow(t))
	exportCSV(&csvBuf, db, filter)

	for name, body := range map[string]string{"json": jsonBuf.String(), "csv": csvBuf.String()} {
		if strings.Contains(body, secret) {
			t.Errorf("%s export carried a credential", name)
		}
		for _, word := range []string{"OPENAI_ADMIN_KEY", "Authorization", "Bearer"} {
			if strings.Contains(body, word) {
				t.Errorf("%s export mentions %q", name, word)
			}
		}
	}
}

func TestExportOnAnEmptyDatabase(t *testing.T) {
	db := testDB(t)
	filter := store.Filter{From: "2026-08-01", To: "2026-08-31"}

	var buf bytes.Buffer
	if err := exportJSON(&buf, db, filter, testWindow(t)); err != nil {
		t.Fatalf("exportJSON on an empty database: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("empty export is not valid JSON: %v", err)
	}

	buf.Reset()
	if err := exportCSV(&buf, db, filter); err != nil {
		t.Fatalf("exportCSV on an empty database: %v", err)
	}
	// A header with no rows is still a valid file a spreadsheet can open.
	if !strings.HasPrefix(buf.String(), "vendor,day,") {
		t.Errorf("empty CSV has no header: %q", buf.String())
	}
}
