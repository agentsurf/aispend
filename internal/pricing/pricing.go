// Package pricing turns unit counts into money, using an effective-dated price
// book compiled into the binary.
//
// It is consulted only where the vendor does not report cost directly. A
// vendor-reported figure is always better than a computed one, and the report
// says which is which — that distinction is what makes a finance person trust
// the number rather than quietly discard it.
package pricing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/prabhuvmk/aispend/internal/fact"
)

//go:embed pricebook.json
var pricebookJSON []byte

// Book is the parsed price book.
type Book struct {
	Version string  `json:"version"`
	Entries []Entry `json:"entries"`
}

// Entry is one model's rates, effective from a date.
//
// Rates are micros per 1,000 units and integers throughout: the whole arithmetic
// path from a token count to a dollar figure stays in integers, because
// floating-point money in a tool whose entire value is a defensible number is an
// unforced error.
type Entry struct {
	Vendor        string `json:"vendor"`
	ModelPattern  string `json:"model"` // glob: "claude-opus-4-*"
	EffectiveFrom string `json:"effective_from"`

	InputMicros      int64 `json:"input_micros_per_1k"`
	OutputMicros     int64 `json:"output_micros_per_1k"`
	CacheReadMicros  int64 `json:"cache_read_micros_per_1k"`
	CacheWriteMicros int64 `json:"cache_write_micros_per_1k"`
}

var book Book

func init() {
	if err := json.Unmarshal(pricebookJSON, &book); err != nil {
		panic(fmt.Sprintf("embedded pricebook.json is invalid: %v", err))
	}
	// Newest effective date first, so the first match for a day is the right
	// one. A stable sort keeps the file's order for entries sharing a date.
	sort.SliceStable(book.Entries, func(i, j int) bool {
		return book.Entries[i].EffectiveFrom > book.Entries[j].EffectiveFrom
	})
}

// Version identifies the price book, stamped onto every fact it prices.
func Version() string { return book.Version }

// Lookup finds the rates for a vendor's model on a given UTC day.
//
// Effective dating matters: when a vendor changes prices, historical costs must
// stay correct, so a fact dated before the change is priced with the entry that
// was in force then.
func Lookup(vendor, model, day string) (Entry, bool) {
	for _, e := range book.Entries {
		if e.Vendor != vendor {
			continue
		}
		if e.EffectiveFrom > day {
			continue // not yet in force on that day
		}
		if match(e.ModelPattern, model) {
			return e, true
		}
	}
	return Entry{}, false
}

// match compares a model against a glob pattern.
func match(pattern, model string) bool {
	if pattern == model {
		return true
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return false
	}
	ok, err := path.Match(pattern, model)
	return err == nil && ok
}

// Price computes what a fact cost, in micros.
//
// Each unit class is priced on its own rate: cache reads at a discount, cache
// writes at a premium, output well above input. Applying one blended rate would
// produce a figure that drifts further from the truth the more a customer
// optimises their caching — which is exactly when they check your work.
func (e Entry) Price(f fact.Fact) int64 {
	return per1k(f.InputUnits, e.InputMicros) +
		per1k(f.OutputUnits, e.OutputMicros) +
		per1k(f.CachedUnits, e.CacheReadMicros) +
		per1k(f.CacheWriteUnits, e.CacheWriteMicros)
}

// per1k multiplies a unit count by a rate quoted per 1,000 units, rounding half
// up in integer arithmetic. Truncating instead would bias every total downward
// by up to a micro per line, which compounds across thousands of rows.
func per1k(units, ratePer1k int64) int64 {
	if units == 0 || ratePer1k == 0 {
		return 0
	}
	return (units*ratePer1k + 500) / 1000
}

// Apply prices a fact that the vendor did not price itself.
//
// A vendor-reported amount is never overwritten. An unknown model is left
// unknown rather than priced at zero: a zero would silently understate the
// total, and the report surfaces the model as a warning instead.
func Apply(f *fact.Fact) (priced bool) {
	if f.AmountBasis != fact.BasisUnknown {
		return false
	}
	entry, ok := Lookup(f.Vendor, f.ModelRef, f.Day)
	if !ok {
		return false
	}

	f.AmountMicros = entry.Price(*f)
	f.AmountBasis = fact.BasisComputed
	f.PriceVersion = book.Version
	return true
}

// UnknownModels lists the models in a set of facts that the price book cannot
// price, so the report can name them rather than quietly showing less spend.
func UnknownModels(facts []fact.Fact) []string {
	seen := map[string]bool{}
	for _, f := range facts {
		if f.AmountBasis != fact.BasisUnknown || f.ModelRef == "" {
			continue
		}
		if _, ok := Lookup(f.Vendor, f.ModelRef, f.Day); !ok {
			seen[f.Vendor+" "+f.ModelRef] = true
		}
	}

	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
