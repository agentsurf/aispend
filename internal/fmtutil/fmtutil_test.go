package fmtutil

import "testing"

func TestTokens(t *testing.T) {
	cases := map[int64]string{
		0: "0", 1: "1", 999: "999",
		1_000: "1.0K", 1_450: "1.5K", 84_210: "84.2K", 847_000: "847K",
		1_000_000: "1.0M", 1_204_331: "1.2M", 421_043_882: "421M",
		1_400_000_000: "1.4B", 12_500_000_000: "12.5B",
	}
	for in, want := range cases {
		if got := Tokens(in); got != want {
			t.Errorf("Tokens(%d) = %q, want %q", in, got, want)
		}
	}
}

// A negative count is not a real quantity — it means something upstream is
// wrong, and printing it as a number would launder a bug into a report.
func TestTokensRejectsNegative(t *testing.T) {
	if got := Tokens(-1); got != Unknown {
		t.Errorf("Tokens(-1) = %q, want %q", got, Unknown)
	}
}

func TestMoney(t *testing.T) {
	cases := map[int64]string{
		0:              "$0.00",
		847_200_000:    "$847.20", // under $1,000: two decimals
		1_000_000:      "$1.00",
		999_990_000:    "$999.99",
		18_392_400_000: "$18,392", // at or above $1,000: no decimals
		15_126_000_000: "$15,126",
		1_000_000_000:  "$1,000",
		-847_200_000:   "-$847.20",
	}
	for in, want := range cases {
		if got := Money(in); got != want {
			t.Errorf("Money(%d) = %q, want %q", in, got, want)
		}
	}
}

// Rounding rather than truncating, so a column of figures still adds up to the
// total a reader computes by hand.
func TestMoneyRoundsRatherThanTruncates(t *testing.T) {
	if got := Money(1_999_600_000); got != "$2,000" {
		t.Errorf("Money(1999.6) = %q, want $2,000", got)
	}
	if got := Money(1_000_400_000); got != "$1,000" {
		t.Errorf("Money(1000.4) = %q, want $1,000", got)
	}
}

// The rule people get wrong: zero and unavailable are different facts, and
// rendering "we couldn't see this" as $0.00 is a silent lie.
func TestUnknownIsNeverZero(t *testing.T) {
	if got := MoneyOrUnknown(0, false); got != Unknown {
		t.Errorf("MoneyOrUnknown(0, false) = %q, want %q", got, Unknown)
	}
	if got := MoneyOrUnknown(0, true); got != "$0.00" {
		t.Errorf("a genuine zero must still print as a zero, got %q", got)
	}
}

func TestThousandsSeparators(t *testing.T) {
	cases := map[int64]string{100: "100", 1_000: "1,000", 15_126: "15,126",
		999_999: "999,999", 1_000_000: "1,000,000"}
	for in, want := range cases {
		if got := group(in); got != want {
			t.Errorf("group(%d) = %q, want %q", in, got, want)
		}
	}
}
