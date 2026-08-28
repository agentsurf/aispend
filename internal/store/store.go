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

	_ "modernc.org/sqlite" // pure Go: no cgo, so cross-compilation stays one command

	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/dbg"
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
