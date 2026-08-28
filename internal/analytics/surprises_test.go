package analytics

import (
	"strings"
	"testing"
	"time"

	"github.com/prabhuvmk/aispend/internal/store"
	"github.com/prabhuvmk/aispend/internal/timerange"
)

func window(t *testing.T) timerange.Range {
	t.Helper()
	r, err := timerange.Parse("14d", time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Each rule gets a case that fires it and one that does not. A rule with only a
// firing test is a rule that will one day fire on everything.
func TestVendorGrowthFiresOnlyOnRealGrowth(t *testing.T) {
	in := Input{
		Window:         window(t),
		Totals:         store.Totals{Micros: 200_000_000, Priced: 10},
		EarlierVendors: map[string]int64{"anthropic": 75_000_000},
		RecentVendors:  map[string]int64{"anthropic": 162_000_000},
	}
	got := vendorWeekOverWeek(in)
	if len(got) != 1 {
		t.Fatalf("a 116%% climb did not fire: %v", got)
	}
	if !strings.Contains(got[0].Text, "anthropic") || !strings.Contains(got[0].Text, "%") {
		t.Errorf("the finding does not name the vendor and the change: %q", got[0].Text)
	}

	// Flat spend is not a finding.
	in.RecentVendors = map[string]int64{"anthropic": 76_000_000}
	if got := vendorWeekOverWeek(in); len(got) != 0 {
		t.Errorf("a 1%% change fired: %v", got)
	}

	// Growth from nothing has no percentage and must not be invented.
	in.EarlierVendors = map[string]int64{"anthropic": 0}
	if got := vendorWeekOverWeek(in); len(got) != 0 {
		t.Errorf("growth from zero produced a percentage: %v", got)
	}
}

func TestDominantPrincipal(t *testing.T) {
	in := Input{
		Totals: store.Totals{Micros: 100_000_000},
		Keys: []store.Group{
			{Key: "apikey_01Rj2N8SVvo6BePZj99NhmiT", Micros: 64_000_000},
			{Key: "key_small", Micros: 4_000_000},
		},
	}
	got := dominantPrincipal(in)
	if len(got) != 1 {
		t.Fatalf("a key at 64%% did not fire: %v", got)
	}
	if strings.Contains(got[0].Text, "apikey_01Rj2N8SVvo6BePZj99NhmiT") {
		t.Errorf("the finding printed a full key identifier: %q", got[0].Text)
	}
	if !strings.Contains(got[0].Text, "64%") {
		t.Errorf("the finding does not give the share: %q", got[0].Text)
	}
}

func TestDominantPrincipalIgnoresAnEvenSpread(t *testing.T) {
	in := Input{Totals: store.Totals{Micros: 100_000_000}}
	for i := 0; i < 20; i++ {
		in.Keys = append(in.Keys, store.Group{Key: "k", Micros: 5_000_000})
	}
	if got := dominantPrincipal(in); len(got) != 0 {
		t.Errorf("an even spread fired: %v", got)
	}
}

func TestUnattributedFiresOnlyAboveHalf(t *testing.T) {
	in := Input{
		Totals:           store.Totals{Micros: 100_000_000},
		Unattributed:     78_000_000,
		UnattributedKeys: 31,
	}
	got := unattributedSpend(in)
	if len(got) != 1 {
		t.Fatalf("78%% unattributed did not fire: %v", got)
	}
	if !strings.Contains(got[0].Text, "31 keys") {
		t.Errorf("the finding does not say how many keys: %q", got[0].Text)
	}

	in.Unattributed = 10_000_000
	if got := unattributedSpend(in); len(got) != 0 {
		t.Errorf("10%% unattributed fired: %v", got)
	}
}

// Ranking is by share of spend, so a rule cannot make itself important by
// shouting: a finding about 2% of the bill sorts below one about 40%.
func TestTopRanksByWeightNotByRuleOrder(t *testing.T) {
	in := Input{
		Totals:           store.Totals{Micros: 100_000_000},
		Keys:             []store.Group{{Key: "big_key_identifier", Micros: 30_000_000}},
		Unattributed:     90_000_000,
		UnattributedKeys: 12,
		EarlierVendors:   map[string]int64{"openai": 1_000_000},
		RecentVendors:    map[string]int64{"openai": 2_000_000},
	}

	got := Top(in, 3)
	if len(got) < 2 {
		t.Fatalf("expected several findings, got %v", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Weight > got[i-1].Weight {
			t.Errorf("findings are not ranked by weight: %+v", got)
			break
		}
	}
}

// When nothing is unusual the block is absent, not an empty heading — a heading
// with nothing under it reads as a tool that found nothing to say.
func TestNothingUnusualProducesNoFindings(t *testing.T) {
	in := Input{
		Totals:         store.Totals{Micros: 100_000_000},
		Keys:           []store.Group{{Key: "a", Micros: 10_000_000}},
		Models:         []store.Group{{Key: "m", Micros: 10_000_000, Units: 1_000_000_000, Facts: 10}},
		Unattributed:   1_000_000,
		EarlierVendors: map[string]int64{"openai": 50_000_000},
		RecentVendors:  map[string]int64{"openai": 50_000_000},
	}
	if got := Top(in, 3); len(got) != 0 {
		t.Errorf("findings on unremarkable data: %+v", got)
	}
}

func TestTopReturnsAtMostN(t *testing.T) {
	in := Input{
		Totals:           store.Totals{Micros: 100_000_000},
		Unattributed:     90_000_000,
		UnattributedKeys: 5,
		EarlierVendors:   map[string]int64{"a": 1_000_000, "b": 1_000_000, "c": 1_000_000},
		RecentVendors:    map[string]int64{"a": 9_000_000, "b": 8_000_000, "c": 7_000_000},
		Keys: []store.Group{
			{Key: "k1", Micros: 40_000_000}, {Key: "k2", Micros: 30_000_000},
		},
	}
	if got := Top(in, 3); len(got) != 3 {
		t.Errorf("Top(3) returned %d findings", len(got))
	}
}

// No finding may name a prompt, a person, or a full key.
func TestFindingsNeverPrintAFullIdentifier(t *testing.T) {
	full := "apikey_01Rj2N8SVvo6BePZj99NhmiT"
	in := Input{
		Totals: store.Totals{Micros: 100_000_000},
		Keys:   []store.Group{{Key: full, Micros: 64_000_000}},
	}
	for _, s := range Top(in, 5) {
		if strings.Contains(s.Text, full) {
			t.Errorf("a finding printed a full identifier: %q", s.Text)
		}
	}
}

// Four keys at 25% each is the least interesting possible data. A fixed
// percentage threshold calls all four dominant and fires four times on it, so
// concentration is measured against the even share instead.
func TestDominantPrincipalIgnoresAnEvenFourWaySplit(t *testing.T) {
	in := Input{Totals: store.Totals{Micros: 100_000_000}}
	for _, k := range []string{"a", "b", "c", "d"} {
		in.Keys = append(in.Keys, store.Group{Key: k, Micros: 25_000_000})
	}
	if got := dominantPrincipal(in); len(got) != 0 {
		t.Errorf("an even four-way split fired %d findings: %v", len(got), got)
	}
}

// One key well clear of its even share still fires.
func TestDominantPrincipalFiresOnRealConcentration(t *testing.T) {
	in := Input{Totals: store.Totals{Micros: 100_000_000}}
	in.Keys = append(in.Keys, store.Group{Key: "dominant", Micros: 64_000_000})
	for _, k := range []string{"b", "c", "d", "e", "f"} {
		in.Keys = append(in.Keys, store.Group{Key: k, Micros: 7_200_000})
	}
	got := dominantPrincipal(in)
	if len(got) != 1 {
		t.Fatalf("real concentration produced %d findings: %v", len(got), got)
	}
}

// The premium-model rule must not fire on ordinary mid-tier spend. The
// threshold is micros per million tokens, and getting the magnitude wrong by a
// factor of a thousand makes the rule fire on everything.
func TestPremiumModelRuleIgnoresOrdinaryRates(t *testing.T) {
	// $56 for 30.8M tokens is about $1.82/MTok — mid-tier, not premium.
	in := Input{Models: []store.Group{
		{Key: "anthropic/claude-sonnet-4.6", Micros: 56_000_000, Units: 30_800_000, Facts: 56},
	}}
	if got := premiumModelOnSmallRequests(in); len(got) != 0 {
		t.Errorf("an ordinary rate fired the premium rule: %v", got)
	}
}

func TestPremiumModelRuleFiresOnAPremiumRate(t *testing.T) {
	// $200 for 10M tokens is $20/MTok on a small daily volume.
	in := Input{Models: []store.Group{
		{Key: "claude-opus-4-6", Micros: 200_000_000, Units: 10_000_000, Facts: 14},
	}}
	if got := premiumModelOnSmallRequests(in); len(got) != 1 {
		t.Errorf("a premium rate on small volumes did not fire: %v", got)
	}
}

// With few keys the even share is large, and twice it exceeds 100% — a
// threshold no key could ever cross. The ceiling keeps the rule fireable.
func TestDominantPrincipalStillFiresWithTwoKeys(t *testing.T) {
	in := Input{
		Totals: store.Totals{Micros: 100_000_000},
		Keys: []store.Group{
			{Key: "dominant", Micros: 64_000_000},
			{Key: "other", Micros: 36_000_000},
		},
	}
	if got := dominantPrincipal(in); len(got) != 1 {
		t.Errorf("a key at 64%% of two did not fire: %v", got)
	}
}
