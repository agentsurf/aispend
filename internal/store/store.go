// Package store owns the SQLite database: opening it, migrating it, and the
// handful of queries that report its health.
//
// It holds collected usage — aggregate token counts and costs — and nothing
// else. No credential reaches this package, by construction: nothing here takes
// one as an argument.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go: no cgo, so cross-compilation stays one command

	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/dbg"
	"github.com/prabhuvmk/aispend/internal/fact"
)

// DB is an open, migrated database.
type DB struct {
	sql  *sql.DB
	path string
}

// Open opens the database at path, creating and migrating it if needed. The
// file is created at 0600 before SQLite touches it, so there is no window in
// which it exists world-readable.
func Open(path string) (*DB, error) {
	if err := precreate(path); err != nil {
		return nil, err
	}

	// _txlock=immediate takes the write lock when a transaction begins rather
	// than on first write, which turns a lock conflict into a clean error at a
	// predictable point instead of a surprise mid-batch.
	sqlDB, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", config.Display(path), err)
	}
	// One writer. A CLI has no concurrency to gain here, and serialising removes
	// a whole class of SQLITE_BUSY that would only appear under load.
	sqlDB.SetMaxOpenConns(1)

	db := &DB{sql: sqlDB, path: path}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func precreate(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, config.FilePerm)
	if err != nil {
		return fmt.Errorf("cannot create %s: %w", config.Display(path), err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	// An existing file may predate this rule, or have been copied in.
	return os.Chmod(path, config.FilePerm)
}

// Close releases the database.
func (d *DB) Close() error { return d.sql.Close() }

// SQL exposes the handle for packages that own their own queries.
func (d *DB) SQL() *sql.DB { return d.sql }

// Path is where the database lives on disk.
func (d *DB) Path() string { return d.path }

// migrate brings the schema up to the current version, one migration per
// transaction, so a failure leaves the database at the last good version rather
// than half-way through.
func (d *DB) migrate() error {
	current, err := d.userVersion()
	if err != nil {
		return err
	}
	if current > schemaVersion() {
		return fmt.Errorf(
			"database at %s is schema v%d, but this build only understands v%d — upgrade aispend",
			config.Display(d.path), current, schemaVersion())
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		dbg.Printf("migrating database to schema v%d", m.version)

		tx, err := d.sql.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.stmts); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration to v%d failed: %w", m.version, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}

	if current == schemaVersion() {
		dbg.Printf("database already at schema v%d — nothing to migrate", current)
	}
	return d.ensureMeta()
}

func (d *DB) userVersion() (int, error) {
	var v int
	if err := d.sql.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("cannot read schema version: %w", err)
	}
	return v, nil
}

// ensureMeta records the schema version and a random install id.
//
// The install id identifies this database, not the person using it: it is
// generated locally, derived from nothing, and exists so a future server can
// recognise repeat submissions from one agent. Nothing transmits it in v1.
func (d *DB) ensureMeta() error {
	if _, err := d.sql.Exec(
		`INSERT INTO meta (k, v) VALUES ('schema_version', ?)
		 ON CONFLICT(k) DO UPDATE SET v = excluded.v`,
		fmt.Sprint(schemaVersion())); err != nil {
		return err
	}

	var existing string
	err := d.sql.QueryRow("SELECT v FROM meta WHERE k = 'install_id'").Scan(&existing)
	switch {
	case err == nil && existing != "":
		return nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("cannot generate install id: %w", err)
	}
	id := hex.EncodeToString(buf)
	dbg.Printf("generated install_id %s", id)
	_, err = d.sql.Exec("INSERT INTO meta (k, v) VALUES ('install_id', ?)", id)
	return err
}

// InstallID returns this database's install id.
func (d *DB) InstallID() (string, error) {
	var v string
	err := d.sql.QueryRow("SELECT v FROM meta WHERE k = 'install_id'").Scan(&v)
	return v, err
}

// Health is what doctor reports about the database.
type Health struct {
	SchemaVersion int
	Facts         int
	Connections   int
	CoveredFrom   string
	CoveredTo     string
}

// Health summarises the database in one query pass.
func (d *DB) Health() (Health, error) {
	var h Health
	var err error

	if h.SchemaVersion, err = d.userVersion(); err != nil {
		return h, err
	}
	if err = d.sql.QueryRow("SELECT count(*) FROM usage_fact").Scan(&h.Facts); err != nil {
		return h, err
	}
	if err = d.sql.QueryRow("SELECT count(*) FROM connection").Scan(&h.Connections); err != nil {
		return h, err
	}
	// COALESCE, because min() over an empty table is NULL and an empty database
	// is the normal state on a first run, not an error.
	if err = d.sql.QueryRow(
		`SELECT COALESCE(min(day), ''), COALESCE(max(day), '') FROM usage_fact`,
	).Scan(&h.CoveredFrom, &h.CoveredTo); err != nil {
		return h, err
	}
	return h, nil
}

// SyncState is how far a vendor's collection got.
type SyncState struct {
	Vendor      string
	CoveredFrom string
	CoveredTo   string
	Cursor      string
	LastRunAt   int64
	LastError   string
}

// SyncState reads one vendor's progress. A vendor never collected returns the
// zero value and no error: not having run yet is a state, not a failure.
func (d *DB) SyncState(vendor string) (SyncState, error) {
	s := SyncState{Vendor: vendor}
	var lastRun sql.NullInt64

	err := d.sql.QueryRow(
		`SELECT covered_from, covered_to, cursor, last_run_at, last_error
		 FROM sync_state WHERE vendor = ?`, vendor,
	).Scan(&s.CoveredFrom, &s.CoveredTo, &s.Cursor, &lastRun, &s.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.LastRunAt = lastRun.Int64
	return s, nil
}

// SaveSyncState records progress. Coverage only ever widens: a 7-day scan after
// a 30-day one must not shrink what the database is known to hold.
func (d *DB) SaveSyncState(s SyncState) error {
	_, err := d.sql.Exec(`
		INSERT INTO sync_state (vendor, covered_from, covered_to, cursor, last_run_at, last_error)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(vendor) DO UPDATE SET
			covered_from = CASE
				WHEN excluded.covered_from = '' THEN sync_state.covered_from
				WHEN sync_state.covered_from = '' THEN excluded.covered_from
				WHEN excluded.covered_from < sync_state.covered_from THEN excluded.covered_from
				ELSE sync_state.covered_from END,
			covered_to = CASE
				WHEN excluded.covered_to > sync_state.covered_to THEN excluded.covered_to
				ELSE sync_state.covered_to END,
			cursor       = excluded.cursor,
			last_run_at  = excluded.last_run_at,
			last_error   = excluded.last_error`,
		s.Vendor, s.CoveredFrom, s.CoveredTo, s.Cursor, s.LastRunAt, s.LastError)
	return err
}

// Totals is what the report's headline number is built from.
type Totals struct {
	Micros int64 // sum of amounts aispend could determine
	Facts  int   // rows in the window
	Priced int   // rows carrying a known amount
	Days   int   // distinct days with data
}

// Filter narrows a report to a window and optionally one vendor.
type Filter struct {
	From   string
	To     string
	Vendor string // empty means every vendor
}

func (f Filter) args() []any {
	return []any{f.From, f.To, f.From, f.To, f.Vendor, f.Vendor}
}

// vendorClause is appended inside the latest CTE's consumers. The vendor is a
// bound parameter, never interpolated.
const vendorClause = ` WHERE (? = '' OR vendor = ?)`

// Totals sums the latest revision of every fact in the window.
//
// Only the highest revision of each fact counts. Summing every revision would
// double-count every day a vendor has ever restated, which is the kind of error
// that is invisible until someone reconciles against an invoice.
func (d *DB) Totals(f Filter) (Totals, error) {
	var t Totals
	err := d.sql.QueryRow(latestCTE+`
		SELECT
		  COALESCE(sum(CASE WHEN amount_basis <> 'unknown' THEN amount_micros ELSE 0 END), 0),
		  count(*),
		  COALESCE(sum(CASE WHEN amount_basis <> 'unknown' THEN 1 ELSE 0 END), 0),
		  count(DISTINCT day)
		FROM latest`+vendorClause,
		f.args()...,
	).Scan(&t.Micros, &t.Facts, &t.Priced, &t.Days)
	return t, err
}

// Group is one row of a grouped report.
type Group struct {
	Key string // vendor, model, key, project or day
	// Vendor is which vendor the row belongs to, where the grouping leaves one
	// unambiguous. A key or project belongs to exactly one vendor; a model can
	// appear under several, and then this is empty.
	Vendor string
	Micros int64
	Facts  int
	Priced int
	Units  int64 // input + output + cached + other
	Input  int64
	Output int64
	Cached int64
}

// latestCTE selects one row per fact, at its highest revision.
//
// Every grouped query goes through this. Summing all revisions would
// double-count every day a vendor has ever restated — an error invisible until
// someone reconciles against an invoice.
const latestCTE = `
WITH latest AS (
  SELECT f.* FROM usage_fact f
  JOIN (
    SELECT vendor, day, workspace_ref, principal_ref, model_ref, max(revision) AS revision
    FROM usage_fact WHERE day BETWEEN ? AND ?
    GROUP BY vendor, day, workspace_ref, principal_ref, model_ref
  ) m ON f.vendor=m.vendor AND f.day=m.day AND f.workspace_ref=m.workspace_ref
     AND f.principal_ref=m.principal_ref AND f.model_ref=m.model_ref
     AND f.revision=m.revision
  WHERE f.day BETWEEN ? AND ?
)`

// GroupBy is a dimension the report can break spend down by.
type GroupBy string

const (
	ByVendor  GroupBy = "vendor"
	ByModel   GroupBy = "model"
	ByKey     GroupBy = "key"
	ByProject GroupBy = "project"
	ByDay     GroupBy = "day"
)

// column maps a dimension to its database column. The mapping is a closed set
// rather than string interpolation, so no caller can reach the query text.
func (g GroupBy) column() (string, bool) {
	switch g {
	case ByVendor:
		return "vendor", true
	case ByModel:
		return "model_ref", true
	case ByKey:
		return "principal_ref", true
	case ByProject:
		return "workspace_ref", true
	case ByDay:
		return "day", true
	}
	return "", false
}

// Valid reports whether this is a dimension aispend knows.
func (g GroupBy) Valid() bool { _, ok := g.column(); return ok }

// GroupByNames lists every dimension, for error messages and help text.
func GroupByNames() []string {
	return []string{string(ByVendor), string(ByModel), string(ByKey), string(ByProject), string(ByDay)}
}

// GroupBy aggregates the window along one dimension.
//
// Rows come back ordered by spend descending — people want the biggest line
// first, every time — except for a day breakdown, which is a time series and is
// returned chronologically. A time series sorted by size is not a time series.
func (d *DB) GroupBy(g GroupBy, f Filter) ([]Group, error) {
	col, ok := g.column()
	if !ok {
		return nil, fmt.Errorf("unknown grouping %q (try: %s)", g, strings.Join(GroupByNames(), ", "))
	}

	order := "micros DESC, key ASC"
	if g == ByDay {
		order = "key ASC"
	}

	rows, err := d.sql.Query(latestCTE+`
		SELECT `+col+` AS key,
		  CASE WHEN count(DISTINCT vendor) = 1 THEN min(vendor) ELSE '' END AS vendor,
		  COALESCE(sum(CASE WHEN amount_basis <> 'unknown' THEN amount_micros ELSE 0 END), 0) AS micros,
		  count(*),
		  COALESCE(sum(CASE WHEN amount_basis <> 'unknown' THEN 1 ELSE 0 END), 0),
		  COALESCE(sum(input_units + output_units + cached_units + cache_write_units + other_units), 0),
		  COALESCE(sum(input_units), 0),
		  COALESCE(sum(output_units), 0),
		  COALESCE(sum(cached_units), 0)
		FROM latest`+vendorClause+` GROUP BY `+col+` ORDER BY `+order,
		f.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.Key, &g.Vendor, &g.Micros, &g.Facts, &g.Priced,
			&g.Units, &g.Input, &g.Output, &g.Cached); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// DailyTotals returns spend per day for one vendor, oldest first, with zero
// rows for days that have no data. Sparklines need a dense series: a gap
// silently redrawn as a shorter line would misreport the shape.
func (d *DB) DailyTotals(f Filter) (map[string]int64, error) {
	rows, err := d.sql.Query(latestCTE+`
		SELECT day, COALESCE(sum(CASE WHEN amount_basis <> 'unknown' THEN amount_micros ELSE 0 END), 0)
		FROM latest`+vendorClause+` GROUP BY day`,
		f.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var day string
		var micros int64
		if err := rows.Scan(&day, &micros); err != nil {
			return nil, err
		}
		out[day] = micros
	}
	return out, rows.Err()
}

// BasisSplit is how much of a total each attribution basis accounts for.
//
// This is the trust property from the design made visible: "the vendor told us
// this cost $8,204" and "we computed $3,266 from our price book" are different
// claims, and a report that blurs them is one a careful reader stops believing.
type BasisSplit struct {
	Basis  string
	Micros int64
	Facts  int
}

// BasisBreakdown returns the split, largest first.
func (d *DB) BasisBreakdown(f Filter) ([]BasisSplit, error) {
	rows, err := d.sql.Query(latestCTE+`
		SELECT amount_basis, COALESCE(sum(amount_micros), 0), count(*)
		FROM latest`+vendorClause+`
		GROUP BY amount_basis ORDER BY sum(amount_micros) DESC`, f.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BasisSplit
	for rows.Next() {
		var b BasisSplit
		if err := rows.Scan(&b.Basis, &b.Micros, &b.Facts); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PriceVersions lists the price books that produced computed amounts in the
// window, so the footer can name them.
func (d *DB) PriceVersions(f Filter) ([]string, error) {
	rows, err := d.sql.Query(latestCTE+`
		SELECT DISTINCT price_version FROM latest`+vendorClause+`
		AND price_version <> '' ORDER BY price_version`, f.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// UnpricedModels lists models in the window carrying no cost, so the report can
// name what is missing instead of quietly showing a smaller number.
func (d *DB) UnpricedModels(f Filter) ([]string, error) {
	rows, err := d.sql.Query(latestCTE+`
		SELECT DISTINCT vendor || ' ' || model_ref FROM latest`+vendorClause+`
		AND amount_basis = 'unknown' AND model_ref <> '' ORDER BY 1`, f.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Facts returns every fact in the window, latest revision only, oldest first.
func (d *DB) Facts(f Filter) ([]fact.Fact, error) {
	rows, err := d.sql.Query(latestCTE+`
		SELECT vendor, day, workspace_ref, principal_ref, model_ref,
		       input_units, output_units, cached_units, cache_write_units, other_units,
		       unit_kind, amount_micros, amount_basis, price_version, revision, collected_at
		FROM latest`+vendorClause+`
		ORDER BY day, vendor, model_ref, principal_ref`, f.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []fact.Fact
	for rows.Next() {
		var v fact.Fact
		var basis string
		var collected int64
		if err := rows.Scan(&v.Vendor, &v.Day, &v.WorkspaceRef, &v.PrincipalRef, &v.ModelRef,
			&v.InputUnits, &v.OutputUnits, &v.CachedUnits, &v.CacheWriteUnits, &v.OtherUnits,
			&v.UnitKind, &v.AmountMicros, &basis, &v.PriceVersion, &v.Revision, &collected); err != nil {
			return nil, err
		}
		v.AmountBasis = fact.Basis(basis)
		v.CollectedAt = time.Unix(collected, 0).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}
