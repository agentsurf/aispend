package store

import (
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

func TestOpenCreatesSchemaV1(t *testing.T) {
	db, _ := open(t)

	h, err := db.Health()
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.SchemaVersion != 1 {
		t.Errorf("schema = v%d, want v1", h.SchemaVersion)
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
