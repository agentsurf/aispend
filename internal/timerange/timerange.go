// Package timerange parses the --since flag and turns it into a window of whole
// UTC days.
//
// Timezone bugs in billing tools are endemic and produce off-by-one-day errors
// that destroy credibility, so there is exactly one rule here: everything is
// UTC, days are 'YYYY-MM-DD' strings, and local time appears nowhere.
package timerange

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Range is a closed interval of whole UTC days: both From and To are included.
type Range struct {
	From time.Time // 00:00:00 UTC on the first day
	To   time.Time // 00:00:00 UTC on the last day
	// Label is how the window was requested, for the report header.
	Label string
}

// Parse accepts "7d", "30d", "90d", any "<n>d", or an absolute "YYYY-MM-DD".
//
// A relative window ends on the last *complete* UTC day — yesterday — because a
// partial today would make every comparison against the prior window wrong by
// however many hours have elapsed. The report says so in its footer rather than
// leaving the reader to work it out.
func Parse(s string, now time.Time) (Range, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Range{}, fmt.Errorf("empty time range")
	}

	end := day(now.UTC()).AddDate(0, 0, -1) // yesterday, the last complete day

	if rest, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(rest)
		if err != nil {
			return Range{}, fmt.Errorf("%q is not a number of days — try 7d, 30d, 90d, or a date like 2026-08-01", s)
		}
		if n < 1 {
			return Range{}, fmt.Errorf("--since %s covers no days", s)
		}
		if n > 366 {
			return Range{}, fmt.Errorf("--since %s is more than a year; vendors do not keep usage that long", s)
		}
		return Range{From: end.AddDate(0, 0, -(n - 1)), To: end, Label: fmt.Sprintf("last %d days", n)}, nil
	}

	from, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		return Range{}, fmt.Errorf("%q is not a date — use YYYY-MM-DD, or a window like 30d", s)
	}
	if from.After(end) {
		return Range{}, fmt.Errorf("%s is in the future; there is no usage to collect yet", s)
	}
	return Range{From: from, To: end, Label: "since " + s}, nil
}

// Days is the number of whole days in the window, inclusive of both ends.
func (r Range) Days() int {
	return int(r.To.Sub(r.From).Hours()/24) + 1
}

// FromDay and ToDay are the 'YYYY-MM-DD' forms stored in usage_fact.day.
func (r Range) FromDay() string { return r.From.Format("2006-01-02") }
func (r Range) ToDay() string   { return r.To.Format("2006-01-02") }

// String renders the window for a report header: "29 Jul – 27 Aug 2026".
func (r Range) String() string {
	if r.From.Year() == r.To.Year() {
		return fmt.Sprintf("%s – %s", r.From.Format("2 Jan"), r.To.Format("2 Jan 2006"))
	}
	return fmt.Sprintf("%s – %s", r.From.Format("2 Jan 2006"), r.To.Format("2 Jan 2006"))
}

// PriorFrom and PriorTo describe the window immediately before this one, of the
// same length. Every delta names the window it compares against, so the reader
// is never left guessing what "up 34%" is relative to.
func (r Range) PriorFrom() string {
	return r.From.AddDate(0, 0, -r.Days()).Format("2006-01-02")
}

func (r Range) PriorTo() string {
	return r.From.AddDate(0, 0, -1).Format("2006-01-02")
}

// Each calls fn once per day in the window, oldest first.
func (r Range) Each(fn func(day string)) {
	for d := r.From; !d.After(r.To); d = d.AddDate(0, 0, 1) {
		fn(d.Format("2006-01-02"))
	}
}

func day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
