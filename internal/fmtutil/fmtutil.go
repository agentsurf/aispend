// Package fmtutil holds every rule about how a number is rendered, so no view
// invents its own and none of them drift apart.
//
// The rules come from design §6.2. Run 13 completes the set; what is here is
// what the collectors need to print a fact.
package fmtutil

import (
	"fmt"
	"strconv"
	"strings"
)

// Unknown is what an unavailable figure renders as.
//
// Zero and unavailable are different facts. A tool that renders "we couldn't
// see this" as 0 has silently lied, and one occurrence discovered by a customer
// costs the account — so this is a package-level constant rather than a
// judgement call made per view.
const Unknown = "—"

// Tokens humanises a count to three significant figures: 421M, 1.4B, 847K.
// The exact integer is what goes in --json and in the database.
func Tokens(n int64) string {
	switch {
	case n < 0:
		return Unknown
	case n < 1_000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return sig(n, 1_000, "K")
	case n < 1_000_000_000:
		return sig(n, 1_000_000, "M")
	default:
		return sig(n, 1_000_000_000, "B")
	}
}

// sig renders to three significant figures — 1.5K, 84.2K, 421M — so columns of
// token counts stay the same width and comparable at a glance.
//
// The arithmetic is integer throughout. Dividing in float first gets 1450/1000
// as 1.4499999999999999556, which %.1f renders as "1.4K" where every reader
// expects "1.5K" — the same class of rounding artifact that makes float money
// unacceptable, showing up in a place it is easy to shrug at.
func sig(n, divisor int64, suffix string) string {
	tenths := (n + divisor/20) / (divisor / 10)
	if tenths >= 1_000 {
		// 100 and above: no decimal, or the column gets too wide to scan.
		return strconv.FormatInt((n+divisor/2)/divisor, 10) + suffix
	}
	return fmt.Sprintf("%d.%d%s", tenths/10, tenths%10, suffix)
}

// Money renders micros as USD: two decimals below $1,000, none above, always
// with a thousands separator.
func Money(micros int64) string {
	neg := micros < 0
	if neg {
		micros = -micros
	}

	var s string
	if micros < 1_000*1_000_000 {
		s = fmt.Sprintf("$%s.%02d", group(micros/1_000_000), (micros%1_000_000)/10_000)
	} else {
		// Round to the nearest dollar rather than truncating, so a column of
		// figures still adds up to the total a reader computes by hand.
		dollars := (micros + 500_000) / 1_000_000
		s = "$" + group(dollars)
	}
	if neg {
		return "-" + s
	}
	return s
}

// MoneyOrUnknown renders an amount that may not be known. An amount aispend
// could not determine is never printed as $0.00.
func MoneyOrUnknown(micros int64, known bool) string {
	if !known {
		return Unknown
	}
	return Money(micros)
}

// group inserts thousands separators.
func group(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}

	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
