// Package analytics computes the things in a spend report worth looking at.
//
// The entire validation thesis is whether the number surprises the reader, so
// the surprises are computed explicitly rather than left to be noticed. Each
// rule is a query, a threshold and a sentence; they are ranked, and the top few
// are shown.
package analytics

import (
	"fmt"
	"sort"

	"github.com/prabhuvmk/aispend/internal/fmtutil"
	"github.com/prabhuvmk/aispend/internal/store"
	"github.com/prabhuvmk/aispend/internal/timerange"
)

// Surprise is one finding, with the figures that produced it.
//
// Weight ranks findings against each other. It is a share of total spend, so a
// rule cannot make itself important by shouting: a finding about 2% of the bill
// sorts below one about 40% no matter which rule produced it.
type Surprise struct {
	Text   string
	Weight int64
}

// Rules is every surprise rule, in no particular order — ranking is by weight.
var Rules = []func(Input) []Surprise{
	vendorWeekOverWeek,
	dominantPrincipal,
	premiumModelOnSmallRequests,
	unattributedSpend,
}

// Input is everything the rules read. Gathering it once keeps each rule to a
// threshold and a sentence rather than its own database access.
type Input struct {
	Window     timerange.Range
	Totals     store.Totals
	Vendors    []store.Group
	Models     []store.Group
	Keys       []store.Group
	PriorTotal int64
	// PriorVendors is spend per vendor over the window before this one.
	PriorVendors map[string]int64
	// Unattributed is spend with no owner mapping, and how many keys it covers.
	Unattributed     int64
	UnattributedKeys int
	// RecentVendors and EarlierVendors split the window in half, for
	// within-window trend rules.
	RecentVendors  map[string]int64
	EarlierVendors map[string]int64
}

// Top computes every rule and returns the n most significant findings.
func Top(in Input, n int) []Surprise {
	var all []Surprise
	for _, rule := range Rules {
		all = append(all, rule(in)...)
	}

	sort.SliceStable(all, func(i, j int) bool { return all[i].Weight > all[j].Weight })
	if len(all) > n {
		all = all[:n]
	}
	return all
}

// vendorWeekOverWeek flags a vendor whose spend has climbed sharply within the
// window. This is the single most common reason a bill surprises someone.
func vendorWeekOverWeek(in Input) []Surprise {
	const minGrowthPct = 40

	var out []Surprise
	for vendor, recent := range in.RecentVendors {
		earlier := in.EarlierVendors[vendor]
		if earlier == 0 || recent <= earlier {
			continue
		}
		growth := ((recent - earlier) * 100) / earlier
		if growth < minGrowthPct {
			continue
		}
		out = append(out, Surprise{
			Text: fmt.Sprintf("%s up %d%% in the second half of this range (%s → %s)",
				vendor, growth, fmtutil.Money(earlier), fmtutil.Money(recent)),
			Weight: recent - earlier,
		})
	}
	return out
}

// dominantPrincipal flags a single key carrying an outsized share. Concentration
// is what makes a spend number actionable: one key at 32% is a conversation, an
// even spread across thirty is not.
func dominantPrincipal(in Input) []Surprise {
	const (
		minSharePct       = 20
		timesTheEvenShare = 2
		// With few keys the even share is large, and twice it can exceed 100%,
		// which would make the rule unfireable. Past this point a key is
		// dominant however many others there are.
		maxThresholdPct = 60
	)

	if in.Totals.Micros == 0 || len(in.Keys) == 0 {
		return nil
	}

	// Concentration is relative, not absolute. Four keys at 25% each is an even
	// spread; a fixed 20% threshold calls all four of them dominant and fires
	// four times on the least interesting possible data. A key has to be well
	// clear of its even share to be worth naming.
	evenShare := 100 / len(in.Keys)
	threshold := minSharePct
	if t := evenShare * timesTheEvenShare; t > threshold {
		threshold = t
	}
	if threshold > maxThresholdPct {
		threshold = maxThresholdPct
	}

	var out []Surprise
	for _, k := range in.Keys {
		if k.Key == "" {
			continue
		}
		share := (k.Micros * 100) / in.Totals.Micros
		if int(share) < threshold {
			continue
		}
		out = append(out, Surprise{
			Text: fmt.Sprintf("one key (%s) is %d%% of everything (%s)",
				shorten(k.Key), share, fmtutil.Money(k.Micros)),
			Weight: k.Micros,
		})
	}
	return out
}

// premiumModelOnSmallRequests flags spend on an expensive model whose average
// request is small enough that a cheaper tier would have done.
//
// This is the rule most likely to make someone act, because it names money that
// is being wasted rather than merely spent.
func premiumModelOnSmallRequests(in Input) []Surprise {
	const (
		// Micros per million units. $5/MTok is roughly the frontier tier;
		// below that a model is not "premium" and the rule has nothing to say.
		premiumMicrosPerMTok = 5_000_000
		// Facts are daily aggregates, so this is an average day's volume for
		// one model, one key, one project — not a per-request figure.
		smallDailyTokens = 2_000_000
	)

	var out []Surprise
	for _, m := range in.Models {
		if m.Micros == 0 || m.Units == 0 || m.Facts == 0 {
			continue
		}
		perMTok := (m.Micros * 1_000_000) / m.Units
		if perMTok < premiumMicrosPerMTok {
			continue
		}
		avg := m.Units / int64(m.Facts)
		if avg > smallDailyTokens {
			continue
		}
		out = append(out, Surprise{
			Text: fmt.Sprintf("%s cost %s at a premium rate on comparatively small workloads",
				m.Key, fmtutil.Money(m.Micros)),
			Weight: m.Micros / 2,
		})
	}
	return out
}

// unattributedSpend flags spend nobody owns. It is the product's whole thesis
// stated as a finding.
func unattributedSpend(in Input) []Surprise {
	const minSharePct = 50

	if in.Totals.Micros == 0 || in.Unattributed == 0 {
		return nil
	}
	share := (in.Unattributed * 100) / in.Totals.Micros
	if share < minSharePct {
		return nil
	}
	return []Surprise{{
		Text: fmt.Sprintf("%s (%d%%) across %d keys has no owner mapping",
			fmtutil.Money(in.Unattributed), share, in.UnattributedKeys),
		Weight: in.Unattributed,
	}}
}

// shorten trims an opaque identifier, keeping the tail that distinguishes it.
func shorten(s string) string {
	const max = 14
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:4]) + "…" + string(r[len(r)-4:])
}
