package sink

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/store"
)

func newSink(t *testing.T) (*SQLiteSink, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "aispend.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewSQLite(db.SQL()), db
}

func sample() []fact.Fact {
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	return []fact.Fact{
		{Vendor: "openai", Day: "2026-08-27", ModelRef: "gpt-5.2", UnitKind: "token",
			InputUnits: 1000, AmountMicros: 41_200_000, AmountBasis: fact.BasisVendorReported,
			Revision: 1, CollectedAt: at},
		{Vendor: "anthropic", Day: "2026-08-27", ModelRef: "claude-opus-4-6", UnitKind: "token",
			InputUnits: 2000, AmountMicros: 128_940_000, AmountBasis: fact.BasisVendorReported,
			Revision: 1, CollectedAt: at},
	}
}

func TestWritePersistsFacts(t *testing.T) {
	s, db := newSink(t)
	if err := s.Write(context.Background(), sample()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	h, err := db.Health()
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.Facts != 2 {
		t.Errorf("facts = %d, want 2", h.Facts)
	}
}

// Re-collecting an overlapping window is the normal case — every sync re-pulls a
// trailing week — so writing the same batch twice must change nothing. If this
// fails, every scan inflates the customer's spend.
func TestWriteIsIdempotent(t *testing.T) {
	s, db := newSink(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := s.Write(ctx, sample()); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	h, _ := db.Health()
	if h.Facts != 2 {
		t.Errorf("facts = %d after three identical writes, want 2", h.Facts)
	}

	var total int64
	if err := db.SQL().QueryRow("SELECT COALESCE(sum(amount_micros),0) FROM usage_fact").Scan(&total); err != nil {
		t.Fatal(err)
	}
	if want := int64(41_200_000 + 128_940_000); total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
}

// A fact with no workspace or principal is common — plenty of vendors don't
// report those dimensions. Stored as NULL it would never conflict with itself,
// because NULL != NULL in SQLite, and re-collection would duplicate the row.
func TestWriteDeduplicatesFactsWithMissingDimensions(t *testing.T) {
	s, db := newSink(t)
	ctx := context.Background()

	f := []fact.Fact{{
		Vendor: "openrouter", Day: "2026-08-26", ModelRef: "claude-sonnet-4-6",
		UnitKind: "token", AmountMicros: 2_480_000, AmountBasis: fact.BasisComputed,
		Revision: 1, CollectedAt: time.Now().UTC(),
		// WorkspaceRef and PrincipalRef deliberately left empty.
	}}

	if err := s.Write(ctx, f); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Write(ctx, f); err != nil {
		t.Fatalf("second Write: %v", err)
	}

	h, _ := db.Health()
	if h.Facts != 1 {
		t.Errorf("facts = %d, want 1 — a fact with empty dimensions did not deduplicate", h.Facts)
	}
}

// A batch is atomic: an interrupted collection must never leave a half-written
// day that later looks complete.
func TestWriteRollsBackTheWholeBatch(t *testing.T) {
	s, db := newSink(t)

	batch := sample()
	batch = append(batch, fact.Fact{
		Vendor: "", // violates CHECK (vendor <> '')
		Day:    "2026-08-27", UnitKind: "token", AmountBasis: fact.BasisVendorReported,
		Revision: 1, CollectedAt: time.Now().UTC(),
	})

	if err := s.Write(context.Background(), batch); err == nil {
		t.Fatal("Write accepted a malformed fact")
	}

	h, _ := db.Health()
	if h.Facts != 0 {
		t.Errorf("facts = %d after a failed batch, want 0 — the transaction did not roll back", h.Facts)
	}
}

func TestWriteStoresTheFactID(t *testing.T) {
	s, db := newSink(t)
	f := sample()[:1]
	if err := s.Write(context.Background(), f); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var got string
	if err := db.SQL().QueryRow("SELECT fact_id FROM usage_fact").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != f[0].ID() {
		t.Errorf("stored fact_id = %s, want %s", got, f[0].ID())
	}
}

func TestEmptyBatchIsANoOp(t *testing.T) {
	s, db := newSink(t)
	if err := s.Write(context.Background(), nil); err != nil {
		t.Fatalf("Write(nil): %v", err)
	}
	if h, _ := db.Health(); h.Facts != 0 {
		t.Errorf("facts = %d", h.Facts)
	}
}

// The Privacy footer is generated from this, so it must name a destination
// rather than be decorative.
func TestDescribeNamesTheDestination(t *testing.T) {
	s, _ := newSink(t)
	if s.Describe() == "" {
		t.Error("Describe() is empty; the Privacy footer would have nothing true to say")
	}
	var _ Sink = s // SQLiteSink must satisfy the interface the agent posture uses
}

// A restatement — the vendor reporting different numbers for a day it already
// reported — must append a revision, not overwrite. "Our number changed because
// the vendor restated on the 14th" is an answer; "our number changed" is not.
func TestRestatementAppendsARevision(t *testing.T) {
	s, db := newSink(t)
	ctx := context.Background()

	first := sample()[:1]
	if err := s.Write(ctx, first); err != nil {
		t.Fatalf("Write: %v", err)
	}

	restated := first[0]
	restated.InputUnits = 9999
	if err := s.Write(ctx, []fact.Fact{restated}); err != nil {
		t.Fatalf("restated Write: %v", err)
	}

	var rows, maxRev int
	db.SQL().QueryRow("SELECT count(*), max(revision) FROM usage_fact").Scan(&rows, &maxRev)
	if rows != 2 {
		t.Errorf("rows = %d, want 2 — the original must stay as the audit trail", rows)
	}
	if maxRev != 2 {
		t.Errorf("max revision = %d, want 2", maxRev)
	}

	// The original figure is still on disk.
	var original int64
	db.SQL().QueryRow("SELECT input_units FROM usage_fact WHERE revision = 1").Scan(&original)
	if original != 1000 {
		t.Errorf("revision 1 input_units = %d, want the original 1000", original)
	}
}

// The common case must stay cheap and silent: every sync re-pulls a trailing
// window, so an identical fact arriving again is not a restatement.
func TestIdenticalReCollectionIsNotARestatement(t *testing.T) {
	s, db := newSink(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := s.Write(ctx, sample()); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	var rows, maxRev int
	db.SQL().QueryRow("SELECT count(*), max(revision) FROM usage_fact").Scan(&rows, &maxRev)
	if rows != 2 || maxRev != 1 {
		t.Errorf("rows = %d, max revision = %d; want 2 rows still at revision 1", rows, maxRev)
	}
}

func TestBatcherWritesInGroups(t *testing.T) {
	s, db := newSink(t)
	b := NewBatcher(s, 2)
	ctx := context.Background()

	facts := sample()
	if err := b.Add(ctx, facts[0]); err != nil {
		t.Fatal(err)
	}
	if h, _ := db.Health(); h.Facts != 0 {
		t.Errorf("wrote before the batch was full: %d facts", h.Facts)
	}

	if err := b.Add(ctx, facts[1]); err != nil {
		t.Fatal(err)
	}
	if h, _ := db.Health(); h.Facts != 2 {
		t.Errorf("a full batch was not written: %d facts", h.Facts)
	}
	if b.Pending() != 0 {
		t.Errorf("Pending() = %d after a flush", b.Pending())
	}
}

// A partial batch must not be lost when the collection ends.
func TestBatcherFlushesTheRemainder(t *testing.T) {
	s, db := newSink(t)
	b := NewBatcher(s, 100)
	ctx := context.Background()

	for _, f := range sample() {
		if err := b.Add(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	if h, _ := db.Health(); h.Facts != 0 {
		t.Fatalf("wrote early: %d", h.Facts)
	}
	if err := b.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if h, _ := db.Health(); h.Facts != 2 {
		t.Errorf("facts = %d after flush, want 2", h.Facts)
	}
}

func TestMultiSinkWritesToEvery(t *testing.T) {
	s1, db1 := newSink(t)
	s2, db2 := newSink(t)

	if err := (MultiSink{s1, s2}).Write(context.Background(), sample()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for i, db := range []*store.DB{db1, db2} {
		if h, _ := db.Health(); h.Facts != 2 {
			t.Errorf("sink %d has %d facts, want 2", i, h.Facts)
		}
	}
}

func TestLocalOnlyWhenEverySinkIsLocal(t *testing.T) {
	s, _ := newSink(t)
	if !Local(s) {
		t.Error("a SQLite sink is not local")
	}
	if !Local(MultiSink{s, s}) {
		t.Error("two local sinks are not local")
	}
}
