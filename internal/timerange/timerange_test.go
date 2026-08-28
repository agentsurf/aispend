package timerange

import (
	"testing"
	"time"
)

// The design's own report header is "last 30 days · 29 Jul – 27 Aug 2026" on
// 28 August. A relative window ends yesterday — the last complete UTC day —
// because a partial today makes every comparison against the prior window wrong
// by however many hours have elapsed.
func TestRelativeWindowEndsYesterday(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 30, 0, 0, time.UTC)

	r, err := Parse("30d", now)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.FromDay() != "2026-07-29" || r.ToDay() != "2026-08-27" {
		t.Errorf("30d = %s..%s, want 2026-07-29..2026-08-27", r.FromDay(), r.ToDay())
	}
	if r.Days() != 30 {
		t.Errorf("Days() = %d, want 30", r.Days())
	}
	if got := r.String(); got != "29 Jul – 27 Aug 2026" {
		t.Errorf("String() = %q", got)
	}
}

// Local time appears nowhere: the same instant in any zone yields the same UTC
// window. Timezone bugs in billing tools produce off-by-one-day errors that
// destroy credibility.
func TestWindowIsUTCRegardlessOfLocalZone(t *testing.T) {
	utc := time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC)

	// Same instant, expressed in a zone where it is already the 28th late
	// evening, and one where it is still the 27th.
	for _, zone := range []int{-11 * 3600, 0, 13 * 3600} {
		loc := time.FixedZone("test", zone)
		r, err := Parse("7d", utc.In(loc))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if r.ToDay() != "2026-08-27" {
			t.Errorf("zone %+d: ToDay() = %s, want 2026-08-27", zone/3600, r.ToDay())
		}
	}
}

func TestDayCounts(t *testing.T) {
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	for _, n := range []int{1, 7, 30, 90, 365} {
		r, err := Parse(itoa(n)+"d", now)
		if err != nil {
			t.Fatalf("Parse %dd: %v", n, err)
		}
		if r.Days() != n {
			t.Errorf("%dd → Days() = %d", n, r.Days())
		}
	}
}

func TestAbsoluteDate(t *testing.T) {
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)

	r, err := Parse("2026-08-01", now)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.FromDay() != "2026-08-01" || r.ToDay() != "2026-08-27" {
		t.Errorf("= %s..%s", r.FromDay(), r.ToDay())
	}
	if r.Days() != 27 {
		t.Errorf("Days() = %d, want 27", r.Days())
	}
}

func TestRejectsNonsense(t *testing.T) {
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	for _, in := range []string{"", "  ", "week", "30", "-5d", "0d", "999d", "2026-13-01", "01/08/2026", "2026-09-30"} {
		if r, err := Parse(in, now); err == nil {
			t.Errorf("Parse(%q) = %v, want an error", in, r)
		}
	}
}

// Every error has to tell the user what to type instead.
func TestErrorsSuggestValidInput(t *testing.T) {
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	for _, in := range []string{"week", "01/08/2026"} {
		_, err := Parse(in, now)
		if err == nil {
			t.Fatalf("Parse(%q) succeeded", in)
		}
		if !contains(err.Error(), "30d") && !contains(err.Error(), "YYYY-MM-DD") {
			t.Errorf("Parse(%q) error does not say what to type instead: %v", in, err)
		}
	}
}

func TestEachVisitsEveryDayInOrder(t *testing.T) {
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	r, _ := Parse("3d", now)

	var days []string
	r.Each(func(d string) { days = append(days, d) })

	want := []string{"2026-08-25", "2026-08-26", "2026-08-27"}
	if len(days) != len(want) {
		t.Fatalf("visited %v, want %v", days, want)
	}
	for i := range want {
		if days[i] != want[i] {
			t.Errorf("day %d = %s, want %s", i, days[i], want[i])
		}
	}
}

// A window spanning a month or year boundary must not lose or gain a day.
func TestSpansBoundaries(t *testing.T) {
	r, err := Parse("5d", time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.FromDay() != "2026-12-28" || r.ToDay() != "2027-01-01" {
		t.Errorf("= %s..%s, want 2026-12-28..2027-01-01", r.FromDay(), r.ToDay())
	}
	if got := r.String(); got != "28 Dec 2026 – 1 Jan 2027" {
		t.Errorf("String() = %q", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
