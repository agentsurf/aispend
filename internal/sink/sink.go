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
	"strings"

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
// Two behaviours share this path, and the difference between them is the whole
// point of the revision column:
//
//   - Re-collecting an identical fact is free. The primary key already
//     identifies it, so a scan run twice produces the same row count and the
//     same total. Every sync re-pulls a trailing window, so this is the common
//     case, not an edge case.
//   - A *restatement* — the vendor reporting different numbers for a day it
//     already reported — appends a new revision rather than overwriting. The
//     old figure stays on disk, so "our number changed because the vendor
//     restated on the 14th" is an answer. "Our number changed" is not.
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

	var written, restated int64
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

		if n, _ := res.RowsAffected(); n > 0 {
			written += n
			continue
		}

		// The row already exists. Either it is identical — the common case, and
		// nothing to do — or the vendor has restated the day.
		changed, err := differs(ctx, tx, f)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		if err := insertRevision(ctx, tx, f); err != nil {
			return err
		}
		restated++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cannot commit %d facts: %w", len(facts), err)
	}
	dbg.Printf("sink wrote %d of %d facts (%d already present, %d restated)",
		written, len(facts), int64(len(facts))-written-restated, restated)
	return nil
}

// differs reports whether the stored latest revision of this fact disagrees
// with what the vendor is reporting now.
func differs(ctx context.Context, tx *sql.Tx, f fact.Fact) (bool, error) {
	var in, out, cached, other, amount int64
	var basis string

	err := tx.QueryRowContext(ctx, `
		SELECT input_units, output_units, cached_units, other_units, amount_micros, amount_basis
		FROM usage_fact
		WHERE vendor=? AND day=? AND workspace_ref=? AND principal_ref=? AND model_ref=?
		ORDER BY revision DESC LIMIT 1`,
		f.Vendor, f.Day, f.WorkspaceRef, f.PrincipalRef, f.ModelRef,
	).Scan(&in, &out, &cached, &other, &amount, &basis)
	if err != nil {
		return false, fmt.Errorf("reading existing %s %s: %w", f.Vendor, f.Day, err)
	}

	return in != f.InputUnits || out != f.OutputUnits || cached != f.CachedUnits ||
		other != f.OtherUnits || amount != f.AmountMicros || basis != string(f.AmountBasis), nil
}

// insertRevision appends the restated fact as the next revision, leaving the
// previous one on disk as the audit trail.
func insertRevision(ctx context.Context, tx *sql.Tx, f fact.Fact) error {
	var next int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(max(revision), 0) + 1 FROM usage_fact
		WHERE vendor=? AND day=? AND workspace_ref=? AND principal_ref=? AND model_ref=?`,
		f.Vendor, f.Day, f.WorkspaceRef, f.PrincipalRef, f.ModelRef,
	).Scan(&next); err != nil {
		return err
	}

	dbg.Printf("%s restated %s %s: writing revision %d", f.Vendor, f.Day, f.ModelRef, next)

	_, err := tx.ExecContext(ctx, insertFact,
		f.ID(), f.Vendor, f.Day, f.WorkspaceRef, f.PrincipalRef, f.ModelRef,
		f.InputUnits, f.OutputUnits, f.CachedUnits, f.OtherUnits, f.UnitKind,
		f.AmountMicros, string(f.AmountBasis), f.PriceVersion, next,
		f.CollectedAt.UTC().Unix())
	return err
}

// Flush is a no-op: every Write already committed.
func (s *SQLiteSink) Flush(context.Context) error { return nil }

// Describe reports the destination for the Privacy footer.
func (s *SQLiteSink) Describe() string { return "the local database on this machine" }

// MultiSink writes to several destinations.
//
// v1 never builds one — SQLite is the only sink. It exists because the sidecar
// posture fans the same collector output out to a control plane, and because
// the report's Privacy footer is generated from what the sinks say about
// themselves: with two sinks configured, "nothing left this machine" stops
// being true, and the footer has to stop saying it on its own.
type MultiSink []Sink

func (m MultiSink) Write(ctx context.Context, facts []fact.Fact) error {
	for _, s := range m {
		if err := s.Write(ctx, facts); err != nil {
			return err
		}
	}
	return nil
}

func (m MultiSink) Flush(ctx context.Context) error {
	for _, s := range m {
		if err := s.Flush(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (m MultiSink) Describe() string {
	parts := make([]string, 0, len(m))
	for _, s := range m {
		parts = append(parts, s.Describe())
	}
	return strings.Join(parts, " and ")
}

// Local reports whether every destination is on this machine. The Privacy
// footer asks the sinks rather than assuming, so the claim cannot outlive the
// configuration that made it true.
func Local(s Sink) bool {
	switch v := s.(type) {
	case *SQLiteSink:
		return true
	case MultiSink:
		for _, child := range v {
			if !Local(child) {
				return false
			}
		}
		return true
	default:
		// An unrecognised sink is assumed to leave the machine. Guessing the
		// other way would let a new sink silently inherit the privacy claim.
		return false
	}
}

// Batcher buffers facts and writes them in groups, so a long backfill does not
// hold everything in memory and a partial failure still persists what it got.
type Batcher struct {
	sink Sink
	size int
	buf  []fact.Fact
}

// NewBatcher wraps a sink. size is the number of facts per transaction.
func NewBatcher(s Sink, size int) *Batcher {
	if size < 1 {
		size = 500
	}
	return &Batcher{sink: s, size: size}
}

// Add buffers one fact, writing when the batch is full.
func (b *Batcher) Add(ctx context.Context, f fact.Fact) error {
	b.buf = append(b.buf, f)
	if len(b.buf) < b.size {
		return nil
	}
	return b.Flush(ctx)
}

// Flush writes whatever is buffered.
func (b *Batcher) Flush(ctx context.Context) error {
	if len(b.buf) == 0 {
		return nil
	}
	batch := b.buf
	b.buf = nil
	return b.sink.Write(ctx, batch)
}

// Pending is how many facts are buffered but not yet written.
func (b *Batcher) Pending() int { return len(b.buf) }
