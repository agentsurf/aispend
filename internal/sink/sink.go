// Package sink is where collected facts go.
//
// It is an interface rather than a direct database call, and that is the whole
// point: in v1 the only implementation writes to SQLite, but the sidecar posture
// (design §9.2) fans the same collector output out to a second destination with
// no change to any collector. Twenty lines now; a day of untangling if skipped.
package sink

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/prabhuvmk/aispend/internal/dbg"
	"github.com/prabhuvmk/aispend/internal/fact"
)

// Sink accepts batches of facts.
type Sink interface {
	// Write persists a batch. It must be atomic: either every fact in the batch
	// lands or none of them do, so an interrupted collection never leaves a
	// half-written day that later looks complete.
	Write(ctx context.Context, facts []fact.Fact) error

	// Flush completes any buffered work.
	Flush(ctx context.Context) error

	// Describe says, in one clause, where this sink sends data.
	//
	// The report's Privacy footer is generated from this rather than written by
	// hand, so the claim "nothing left this machine" cannot survive the day a
	// second sink is configured. Design §9.4 makes that a test, not a
	// convention.
	Describe() string
}

// SQLiteSink writes facts to the local database. It is the only sink in v1, and
// the reason the Privacy footer can truthfully say nothing left the machine.
type SQLiteSink struct {
	db *sql.DB
}

// NewSQLite returns a sink writing to db.
func NewSQLite(db *sql.DB) *SQLiteSink { return &SQLiteSink{db: db} }

const insertFact = `
INSERT INTO usage_fact (
  fact_id, vendor, day, workspace_ref, principal_ref, model_ref,
  input_units, output_units, cached_units, other_units, unit_kind,
  amount_micros, amount_basis, price_version, revision, collected_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT DO NOTHING`

// Write inserts a batch in one transaction.
//
// ON CONFLICT DO NOTHING makes re-collection of an overlapping window free: the
// primary key already identifies the fact, so a scan run twice produces the same
// row count and the same total. Restatements — where the vendor reports
// different numbers for a day it already reported — append a new revision
// instead, which arrives with the backfill logic that needs it.
func (s *SQLiteSink) Write(ctx context.Context, facts []fact.Fact) error {
	if len(facts) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("cannot begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op after a successful commit

	stmt, err := tx.PrepareContext(ctx, insertFact)
	if err != nil {
		return err
	}
	defer stmt.Close()

	var written int64
	for _, f := range facts {
		res, err := stmt.ExecContext(ctx,
			f.ID(), f.Vendor, f.Day, f.WorkspaceRef, f.PrincipalRef, f.ModelRef,
			f.InputUnits, f.OutputUnits, f.CachedUnits, f.OtherUnits, f.UnitKind,
			f.AmountMicros, string(f.AmountBasis), f.PriceVersion, f.Revision,
			f.CollectedAt.UTC().Unix(),
		)
		if err != nil {
			return fmt.Errorf("writing %s %s %s: %w", f.Vendor, f.Day, f.ModelRef, err)
		}
		n, _ := res.RowsAffected()
		written += n
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cannot commit %d facts: %w", len(facts), err)
	}
	dbg.Printf("sink wrote %d of %d facts (%d were already present)",
		written, len(facts), int64(len(facts))-written)
	return nil
}

// Flush is a no-op: every Write already committed.
func (s *SQLiteSink) Flush(context.Context) error { return nil }

// Describe reports the destination for the Privacy footer.
func (s *SQLiteSink) Describe() string { return "the local database on this machine" }
