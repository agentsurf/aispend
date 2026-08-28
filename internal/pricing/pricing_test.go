package pricing

import (
	"testing"

	"github.com/prabhuvmk/aispend/internal/fact"
)

func TestEmbeddedBookLoads(t *testing.T) {
	if Version() == "" {
		t.Error("no price book version")
	}
	if len(book.Entries) == 0 {
		t.Fatal("no price book entries")
	}
	for _, e := range book.Entries {
		if e.Vendor == "" || e.ModelPattern == "" || e.EffectiveFrom == "" {
			t.Errorf("incomplete entry: %+v", e)
		}
		if e.InputMicros <= 0 || e.OutputMicros <= 0 {
			t.Errorf("%s %s has no input/output rate", e.Vendor, e.ModelPattern)
		}
	}
}

func TestLookupMatchesGlobs(t *testing.T) {
	if _, ok := Lookup("anthropic", "claude-opus-4-6", "2026-08-27"); !ok {
		t.Error("claude-opus-4-6 did not match claude-opus-4-*")
	}
	if _, ok := Lookup("anthropic", "claude-sonnet-4-6", "2026-08-27"); !ok {
		t.Error("claude-sonnet-4-6 not found")
	}
	if _, ok := Lookup("openai", "gpt-5.2", "2026-08-27"); !ok {
		t.Error("gpt-5.2 not found")
	}
	// A model the book does not know stays unknown rather than matching
	// something adjacent.
	if _, ok := Lookup("openai", "some-future-model", "2026-08-27"); ok {
		t.Error("an unknown model matched an entry")
	}
	// Vendors do not share a namespace.
	if _, ok := Lookup("openai", "claude-opus-4-6", "2026-08-27"); ok {
		t.Error("a model matched under the wrong vendor")
	}
}

// Historical costs must stay correct when a vendor changes prices, so a fact is
// priced with the entry in force on its own day, not today's.
func TestEffectiveDating(t *testing.T) {
	saved := book.Entries
	defer func() { book.Entries = saved }()

	book.Entries = []Entry{
		{Vendor: "v", ModelPattern: "m", EffectiveFrom: "2026-06-01",
			InputMicros: 2000, OutputMicros: 4000},
		{Vendor: "v", ModelPattern: "m", EffectiveFrom: "2026-01-01",
			InputMicros: 1000, OutputMicros: 2000},
	}

	before, ok := Lookup("v", "m", "2026-03-15")
	if !ok || before.InputMicros != 1000 {
		t.Errorf("a March fact was priced at %d, want the older 1000", before.InputMicros)
	}
	after, ok := Lookup("v", "m", "2026-07-15")
	if !ok || after.InputMicros != 2000 {
		t.Errorf("a July fact was priced at %d, want the newer 2000", after.InputMicros)
	}
	if _, ok := Lookup("v", "m", "2025-12-31"); ok {
		t.Error("a fact predating every entry was priced anyway")
	}
}

// Each unit class carries its own rate. One blended rate would drift further
// from the truth the more a customer optimises their caching.
func TestPriceUsesEveryUnitClass(t *testing.T) {
	e := Entry{InputMicros: 1000, OutputMicros: 2000, CacheReadMicros: 100, CacheWriteMicros: 1250}

	f := fact.Fact{InputUnits: 1000, OutputUnits: 1000, CachedUnits: 1000, CacheWriteUnits: 1000}
	// 1000 units at each rate = one thousandth of each rate, times 1000 units.
	want := int64(1000 + 2000 + 100 + 1250)
	if got := e.Price(f); got != want {
		t.Errorf("Price = %d, want %d", got, want)
	}

	// Cache reads must cost less than uncached input, and writes more.
	read := e.Price(fact.Fact{CachedUnits: 1_000_000})
	input := e.Price(fact.Fact{InputUnits: 1_000_000})
	write := e.Price(fact.Fact{CacheWriteUnits: 1_000_000})
	if !(read < input && input < write) {
		t.Errorf("rates are not ordered read(%d) < input(%d) < write(%d)", read, input, write)
	}
}

// Integer arithmetic throughout, rounding half up. Truncating would bias every
// total downward by up to a micro per line, compounding across thousands.
func TestPer1kRoundsHalfUp(t *testing.T) {
	cases := map[[2]int64]int64{
		{1000, 1250}: 1250,
		{1, 1000}:    1,
		{1, 1500}:    2, // 1.5 rounds up
		{1, 400}:     0, // 0.4 rounds down
		{1, 500}:     1, // 0.5 rounds up
		{0, 9999}:    0,
	}
	for in, want := range cases {
		if got := per1k(in[0], in[1]); got != want {
			t.Errorf("per1k(%d, %d) = %d, want %d", in[0], in[1], got, want)
		}
	}
}

// A vendor-reported figure is always better than a computed one and must never
// be overwritten.
func TestApplyNeverOverwritesAVendorReportedAmount(t *testing.T) {
	f := fact.Fact{
		Vendor: "openai", ModelRef: "gpt-5.2", Day: "2026-08-27",
		InputUnits: 1_000_000, AmountMicros: 8_204_000_000,
		AmountBasis: fact.BasisVendorReported,
	}
	if Apply(&f) {
		t.Error("Apply repriced a vendor-reported fact")
	}
	if f.AmountMicros != 8_204_000_000 || f.AmountBasis != fact.BasisVendorReported {
		t.Errorf("the vendor's own figure was changed: %+v", f)
	}
}

// An unknown model is left unknown, not priced at zero: a zero would silently
// understate the total, and "we could not price this" is a fact the report has
// to state.
func TestApplyLeavesAnUnknownModelUnpriced(t *testing.T) {
	f := fact.Fact{
		Vendor: "openai", ModelRef: "gpt-99-imaginary", Day: "2026-08-27",
		InputUnits: 1_000_000, AmountBasis: fact.BasisUnknown,
	}
	if Apply(&f) {
		t.Error("an unknown model was priced")
	}
	if f.AmountBasis != fact.BasisUnknown {
		t.Errorf("AmountBasis = %q, want it left unknown", f.AmountBasis)
	}
	if f.AmountMicros != 0 {
		t.Errorf("AmountMicros = %d for an unpriceable fact", f.AmountMicros)
	}
}

func TestApplyStampsThePriceBookVersion(t *testing.T) {
	f := fact.Fact{
		Vendor: "openai", ModelRef: "gpt-5.2", Day: "2026-08-27",
		InputUnits: 1_000_000, AmountBasis: fact.BasisUnknown,
	}
	if !Apply(&f) {
		t.Fatal("a known model was not priced")
	}
	if f.AmountBasis != fact.BasisComputed {
		t.Errorf("AmountBasis = %q, want computed", f.AmountBasis)
	}
	if f.PriceVersion != Version() {
		t.Errorf("PriceVersion = %q, want %q — a computed amount must say which book produced it",
			f.PriceVersion, Version())
	}
	if f.AmountMicros != 1_250_000 {
		t.Errorf("AmountMicros = %d, want 1250000", f.AmountMicros)
	}
}

func TestUnknownModelsAreReported(t *testing.T) {
	facts := []fact.Fact{
		{Vendor: "openai", ModelRef: "gpt-5.2", Day: "2026-08-27", AmountBasis: fact.BasisUnknown},
		{Vendor: "openai", ModelRef: "gpt-99-imaginary", Day: "2026-08-27", AmountBasis: fact.BasisUnknown},
		{Vendor: "openai", ModelRef: "gpt-99-imaginary", Day: "2026-08-26", AmountBasis: fact.BasisUnknown},
	}
	got := UnknownModels(facts)
	if len(got) != 1 || got[0] != "openai gpt-99-imaginary" {
		t.Errorf("UnknownModels = %v, want the one unpriceable model", got)
	}
}
