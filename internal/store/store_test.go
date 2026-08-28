package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aispend.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func TestOpenCreatesTheCurrentSchema(t *testing.T) {
	db, _ := open(t)

	h, err := db.Health()
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.SchemaVersion != schemaVersion() {
		t.Errorf("schema = v%d, want v%d", h.SchemaVersion, schemaVersion())
	}
	if h.Facts != 0 || h.Connections != 0 {
		t.Errorf("fresh database is not empty: %+v", h)
	}

	rows, err := db.SQL().Query(
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	if got := strings.Join(tables, " "); got != "connection meta sync_state usage_fact" {
		t.Errorf("tables = %q", got)
	}
}

func TestDatabaseFileIs0600(t *testing.T) {
	_, path := open(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %#o, want 0600", perm)
	}
}

// An existing database that is somehow group- or world-readable gets tightened
// rather than merely tolerated.
func TestOpenTightensLoosePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aispend.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %#o, want 0600", perm)
	}
}

// Opening twice must not re-run migrations or regenerate the install id: the
// second open is the common case, and a re-migration would be a data-loss bug
// hiding behind a working command.
func TestMigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aispend.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id1, err := first.InstallID()
	if err != nil {
		t.Fatalf("InstallID: %v", err)
	}
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	id2, err := second.InstallID()
	if err != nil {
		t.Fatalf("InstallID: %v", err)
	}
	if id1 != id2 {
		t.Errorf("install id changed on reopen: %s then %s", id1, id2)
	}
	if id1 == "" {
		t.Error("install id is empty")
	}
}

// A database written by a newer aispend must be refused with an actionable
// message rather than silently misread.
func TestRefusesANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aispend.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.SQL().Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = Open(path)
	if err == nil {
		t.Fatal("Open accepted a v99 database")
	}
	if !strings.Contains(err.Error(), "upgrade aispend") {
		t.Errorf("error does not tell the user what to do: %v", err)
	}
}

// STRICT tables are what stop amount_micros quietly becoming a float — the
// integer-money rule enforced by the schema rather than by discipline.
func TestMoneyColumnRejectsAFloat(t *testing.T) {
	db, _ := open(t)
	_, err := db.SQL().Exec(`
		INSERT INTO usage_fact (fact_id, vendor, day, unit_kind, amount_micros, amount_basis, collected_at)
		VALUES ('x', 'openai', '2026-08-27', 'token', 41.2, 'vendor_reported', 0)`)
	if err == nil {
		t.Fatal("a float was accepted into amount_micros")
	}
}

func TestChecksRejectMalformedFacts(t *testing.T) {
	db, _ := open(t)
	cases := map[string]string{
		"empty vendor":  `('a','','2026-08-27','token',1,'vendor_reported',0)`,
		"short day":     `('a','openai','2026-8-7','token',1,'vendor_reported',0)`,
		"no unit kind":  `('a','openai','2026-08-27','',1,'vendor_reported',0)`,
		"unknown basis": `('a','openai','2026-08-27','token',1,'guessed',0)`,
	}
	for name, values := range cases {
		_, err := db.SQL().Exec(`INSERT INTO usage_fact
			(fact_id, vendor, day, unit_kind, amount_micros, amount_basis, collected_at)
			VALUES ` + values)
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestHealthCoverageOnAnEmptyTable(t *testing.T) {
	db, _ := open(t)
	h, err := db.Health()
	if err != nil {
		t.Fatalf("Health on an empty database: %v", err)
	}
	if h.CoveredFrom != "" || h.CoveredTo != "" {
		t.Errorf("coverage = %q..%q, want empty", h.CoveredFrom, h.CoveredTo)
	}
}

// A vendor that has never collected returns the zero value, not an error: not
// having run yet is a state to report, not a failure.
func TestSyncStateForAnUncollectedVendor(t *testing.T) {
	db, _ := open(t)

	s, err := db.SyncState("openai")
	if err != nil {
		t.Fatalf("SyncState: %v", err)
	}
	if s.Vendor != "openai" || s.Cursor != "" || s.CoveredTo != "" {
		t.Errorf("state = %+v, want the zero value", s)
	}
}

func TestSaveAndReadSyncState(t *testing.T) {
	db, _ := open(t)

	want := SyncState{
		Vendor: "openai", CoveredFrom: "2026-07-29", CoveredTo: "2026-08-27",
		Cursor: "page_abc", LastRunAt: 1787916675,
	}
	if err := db.SaveSyncState(want); err != nil {
		t.Fatalf("SaveSyncState: %v", err)
	}

	got, err := db.SyncState("openai")
	if err != nil {
		t.Fatalf("SyncState: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// Coverage only ever widens. A 7-day scan run after a 30-day one must not
// shrink what the database is known to hold, or the next report would silently
// under-claim its own range.
func TestCoverageOnlyWidens(t *testing.T) {
	db, _ := open(t)

	if err := db.SaveSyncState(SyncState{
		Vendor: "openai", CoveredFrom: "2026-07-29", CoveredTo: "2026-08-27",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSyncState(SyncState{
		Vendor: "openai", CoveredFrom: "2026-08-21", CoveredTo: "2026-08-27",
	}); err != nil {
		t.Fatal(err)
	}

	got, _ := db.SyncState("openai")
	if got.CoveredFrom != "2026-07-29" {
		t.Errorf("CoveredFrom = %s, want the earlier 2026-07-29", got.CoveredFrom)
	}

	// A later end date does extend it.
	db.SaveSyncState(SyncState{Vendor: "openai", CoveredFrom: "2026-08-21", CoveredTo: "2026-08-28"})
	got, _ = db.SyncState("openai")
	if got.CoveredTo != "2026-08-28" {
		t.Errorf("CoveredTo = %s, want the later 2026-08-28", got.CoveredTo)
	}
}

func insertFact(t *testing.T, db *DB, day string, revision int, micros int64, basis string) {
	t.Helper()
	_, err := db.SQL().Exec(`
		INSERT INTO usage_fact (fact_id, vendor, day, workspace_ref, principal_ref, model_ref,
			input_units, unit_kind, amount_micros, amount_basis, revision, collected_at)
		VALUES ('id', 'openai', ?, '', '', 'gpt-5.2', 100, 'token', ?, ?, ?, 0)`,
		day, micros, basis, revision)
	if err != nil {
		t.Fatal(err)
	}
}

// Only the highest revision of a fact counts. Summing every revision would
// double-count every day a vendor has ever restated — an error invisible until
// someone reconciles against an invoice, which is the worst moment to find it.
func TestTotalsCountOnlyTheLatestRevision(t *testing.T) {
	db, _ := open(t)

	insertFact(t, db, "2026-08-27", 1, 10_000_000, "vendor_reported")
	insertFact(t, db, "2026-08-27", 2, 25_000_000, "vendor_reported") // restated

	got, err := db.Totals(Filter{From: "2026-08-01", To: "2026-08-31"})
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	if got.Micros != 25_000_000 {
		t.Errorf("Micros = %d, want only the latest revision (25000000)", got.Micros)
	}
	if got.Facts != 1 {
		t.Errorf("Facts = %d, want 1", got.Facts)
	}
}

// An unpriced fact contributes nothing to the total and is counted separately,
// so the report can say how much of its own number is missing.
func TestTotalsSeparateUnknownFromZero(t *testing.T) {
	db, _ := open(t)

	insertFact(t, db, "2026-08-26", 1, 0, "unknown")
	insertFact(t, db, "2026-08-27", 1, 5_000_000, "vendor_reported")

	got, _ := db.Totals(Filter{From: "2026-08-01", To: "2026-08-31"})
	if got.Facts != 2 {
		t.Errorf("Facts = %d, want 2", got.Facts)
	}
	if got.Priced != 1 {
		t.Errorf("Priced = %d, want 1", got.Priced)
	}
	if got.Micros != 5_000_000 {
		t.Errorf("Micros = %d, want 5000000", got.Micros)
	}
	if got.Days != 2 {
		t.Errorf("Days = %d, want 2", got.Days)
	}
}

func TestTotalsRespectTheWindow(t *testing.T) {
	db, _ := open(t)

	insertFact(t, db, "2026-07-01", 1, 9_000_000, "vendor_reported")
	insertFact(t, db, "2026-08-27", 1, 5_000_000, "vendor_reported")

	got, _ := db.Totals(Filter{From: "2026-08-01", To: "2026-08-31"})
	if got.Micros != 5_000_000 {
		t.Errorf("Micros = %d — a fact outside the window was counted", got.Micros)
	}
}

// Every migration must apply cleanly on top of the previous schema, not only on
// a fresh database — an installed copy upgrades through them in order, carrying
// its data with it.
func TestMigrationsUpgradeAnExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aispend.db")

	// Build a genuine v1 database: migration 1 only, recorded as version 1.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := raw.Exec(migrations[0].stmts); err != nil {
		t.Fatalf("applying v1: %v", err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		INSERT INTO usage_fact (fact_id, vendor, day, unit_kind, amount_micros, amount_basis, collected_at)
		VALUES ('x','openai','2026-08-27','token',41200000,'vendor_reported',0)`); err != nil {
		t.Fatalf("insert into v1: %v", err)
	}
	raw.Close()

	// Opening it must carry it forward without losing the row.
	db, err := Open(path)
	if err != nil {
		t.Fatalf("upgrading a v1 database: %v", err)
	}
	defer db.Close()

	h, err := db.Health()
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.SchemaVersion != schemaVersion() {
		t.Errorf("schema = v%d after upgrade, want v%d", h.SchemaVersion, schemaVersion())
	}
	if h.Facts != 1 {
		t.Errorf("facts = %d after upgrade — the existing row was lost", h.Facts)
	}

	// The added column exists and defaults sensibly for the pre-existing row.
	var write int64
	if err := db.SQL().QueryRow("SELECT cache_write_units FROM usage_fact").Scan(&write); err != nil {
		t.Fatalf("new column missing after upgrade: %v", err)
	}
	if write != 0 {
		t.Errorf("cache_write_units = %d for a row written before the column existed, want 0", write)
	}
}

// The three input classes are priced differently — uncached, cache writes at a
// premium, cache reads at a discount — so each needs its own column. Combining
// any two moves the number in a direction that cannot be corrected later.
func TestCacheWriteUnitsAreStoredSeparately(t *testing.T) {
	db, _ := open(t)

	if _, err := db.SQL().Exec(`
		INSERT INTO usage_fact (fact_id, vendor, day, model_ref, unit_kind,
			input_units, cached_units, cache_write_units, amount_micros, amount_basis, collected_at)
		VALUES ('x','anthropic','2026-08-27','claude-opus-5','token',1500,200,1500,0,'unknown',0)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var in, read, write int64
	if err := db.SQL().QueryRow(
		"SELECT input_units, cached_units, cache_write_units FROM usage_fact").
		Scan(&in, &read, &write); err != nil {
		t.Fatal(err)
	}
	if in != 1500 || read != 200 || write != 1500 {
		t.Errorf("got in=%d read=%d write=%d; the three classes were not kept apart", in, read, write)
	}
}

func seedGrouped(t *testing.T, db *DB) {
	t.Helper()
	rows := []struct {
		id, vendor, day, ws, key, model string
		micros                          int64
	}{
		{"a", "anthropic", "2026-08-27", "wrkspc_1", "apikey_1", "claude-opus-4-6", 11_790_000},
		{"b", "anthropic", "2026-08-27", "", "apikey_2", "claude-sonnet-4-6", 3_880_000},
		{"c", "openai", "2026-08-26", "proj_a", "key_1", "gpt-5.2", 2_380_000},
		{"d", "openai", "2026-08-26", "proj_b", "key_2", "gpt-5.2-mini", 40_000},
	}
	for _, r := range rows {
		if _, err := db.SQL().Exec(`
			INSERT INTO usage_fact (fact_id, vendor, day, workspace_ref, principal_ref, model_ref,
				input_units, unit_kind, amount_micros, amount_basis, revision, collected_at)
			VALUES (?,?,?,?,?,?,1000,'token',?, 'computed', 1, 0)`,
			r.id, r.vendor, r.day, r.ws, r.key, r.model, r.micros); err != nil {
			t.Fatal(err)
		}
	}
}

// The arithmetic a sceptical reader checks first: every way of slicing the same
// window must add up to the same number. If they disagree, the grouping is
// dropping or duplicating rows.
func TestEveryGroupingReconcilesToTheSameTotal(t *testing.T) {
	db, _ := open(t)
	seedGrouped(t, db)
	f := Filter{From: "2026-08-01", To: "2026-08-31"}

	totals, err := db.Totals(f)
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}

	for _, by := range []GroupBy{ByVendor, ByModel, ByKey, ByProject, ByDay} {
		groups, err := db.GroupBy(by, f)
		if err != nil {
			t.Fatalf("GroupBy(%s): %v", by, err)
		}
		var sum int64
		var facts int
		for _, g := range groups {
			sum += g.Micros
			facts += g.Facts
		}
		if sum != totals.Micros {
			t.Errorf("--by %s sums to %d, but the total is %d", by, sum, totals.Micros)
		}
		if facts != totals.Facts {
			t.Errorf("--by %s covers %d facts, but the window holds %d", by, facts, totals.Facts)
		}
	}
}

// Biggest line first, every time — except a day breakdown, which is a time
// series. A time series sorted by size is not a time series.
func TestGroupOrdering(t *testing.T) {
	db, _ := open(t)
	seedGrouped(t, db)
	f := Filter{From: "2026-08-01", To: "2026-08-31"}

	byModel, _ := db.GroupBy(ByModel, f)
	for i := 1; i < len(byModel); i++ {
		if byModel[i].Micros > byModel[i-1].Micros {
			t.Errorf("model rows are not descending by spend: %+v", byModel)
			break
		}
	}

	byDay, _ := db.GroupBy(ByDay, f)
	for i := 1; i < len(byDay); i++ {
		if byDay[i].Key < byDay[i-1].Key {
			t.Errorf("day rows are not chronological: %+v", byDay)
			break
		}
	}
}

func TestGroupByRejectsAnUnknownDimension(t *testing.T) {
	db, _ := open(t)
	_, err := db.GroupBy(GroupBy("nonsense"), Filter{From: "2026-08-01", To: "2026-08-31"})
	if err == nil {
		t.Fatal("an unknown dimension was accepted")
	}
	for _, name := range GroupByNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not list %q as a valid value: %v", name, err)
		}
	}
}

func TestFilterByVendor(t *testing.T) {
	db, _ := open(t)
	seedGrouped(t, db)

	all, _ := db.Totals(Filter{From: "2026-08-01", To: "2026-08-31"})
	one, _ := db.Totals(Filter{From: "2026-08-01", To: "2026-08-31", Vendor: "openai"})

	if one.Micros >= all.Micros {
		t.Errorf("--vendor did not narrow the total: %d vs %d", one.Micros, all.Micros)
	}
	if one.Micros != 2_380_000+40_000 {
		t.Errorf("openai total = %d, want 2420000", one.Micros)
	}
}

func TestBasisBreakdownSeparatesTheClaims(t *testing.T) {
	db, _ := open(t)
	insertFact(t, db, "2026-08-27", 1, 8_204_000_000, "vendor_reported")
	if _, err := db.SQL().Exec(`
		INSERT INTO usage_fact (fact_id, vendor, day, model_ref, unit_kind,
			amount_micros, amount_basis, revision, collected_at)
		VALUES ('other','openai','2026-08-26','m2','token',3_266_000_000,'computed',1,0)`); err != nil {
		t.Fatal(err)
	}

	splits, err := db.BasisBreakdown(Filter{From: "2026-08-01", To: "2026-08-31"})
	if err != nil {
		t.Fatalf("BasisBreakdown: %v", err)
	}
	if len(splits) != 2 {
		t.Fatalf("got %d bases, want 2: %+v", len(splits), splits)
	}
	// Largest first, and the two must sum to the total.
	if splits[0].Basis != "vendor_reported" || splits[0].Micros != 8_204_000_000 {
		t.Errorf("first split = %+v", splits[0])
	}

	totals, _ := db.Totals(Filter{From: "2026-08-01", To: "2026-08-31"})
	if splits[0].Micros+splits[1].Micros != totals.Micros {
		t.Errorf("the basis split does not reconcile to the total")
	}
}
