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
