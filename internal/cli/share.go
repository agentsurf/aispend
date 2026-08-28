package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/prabhuvmk/aispend/internal/buildinfo"
	"github.com/prabhuvmk/aispend/internal/owners"
	"github.com/prabhuvmk/aispend/internal/store"
	"github.com/prabhuvmk/aispend/internal/timerange"
	"github.com/prabhuvmk/aispend/internal/ui"
)

// shareBlock is the shape of someone's AI spend without any of the amounts.
//
// The tool sends nothing, which is the whole point — but it means we learn
// nothing unless the person tells us. This is the answer: a block of ratios and
// counts that most people will paste into a reply without hesitation, which is
// the only way to get aggregate signal across ten prospects from a binary that
// phones nobody.
type shareBlock struct {
	Version      string
	Window       string
	Vendors      int
	VendorMix    string
	Models       int
	Keys         int
	Unattributed int
	Change       string
	Surprised    string
}

func renderShare(w io.Writer, caps ui.Caps, db *store.DB, r timerange.Range, surprised string) error {
	filter := store.Filter{From: r.FromDay(), To: r.ToDay()}

	totals, err := db.Totals(filter)
	if err != nil {
		return err
	}
	if totals.Facts == 0 {
		return fmt.Errorf("nothing collected yet\n\n  Run:  aispend scan")
	}

	vendors, err := db.GroupBy(store.ByVendor, filter)
	if err != nil {
		return err
	}
	models, err := db.GroupBy(store.ByModel, filter)
	if err != nil {
		return err
	}
	keys, err := db.GroupBy(store.ByKey, filter)
	if err != nil {
		return err
	}

	// Vendor mix as percentages, largest first. A share is not an amount: it
	// says how a bill is split without saying how big it is.
	//
	// Sorted numerically. Sorting the rendered strings puts 9 ahead of 78,
	// which is the same class of bug as comparing version numbers as text.
	var shares []int
	for _, v := range vendors {
		if totals.Micros == 0 {
			break
		}
		shares = append(shares, int((v.Micros*100+totals.Micros/2)/totals.Micros))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(shares)))

	mix := make([]string, 0, len(shares))
	for _, s := range shares {
		mix = append(mix, fmt.Sprint(s))
	}

	m, err := loadOwners()
	if err != nil {
		return err
	}
	var unattributed int64
	for _, k := range keys {
		if m.Team(k.Vendor, k.Key) == owners.Unattributed {
			unattributed += k.Micros
		}
	}
	unattributedPct := 0
	if totals.Micros > 0 {
		unattributedPct = int((unattributed*100 + totals.Micros/2) / totals.Micros)
	}

	change := "n/a"
	if prior, err := db.Totals(store.Filter{From: r.PriorFrom(), To: r.PriorTo()}); err == nil && prior.Micros > 0 {
		change = fmt.Sprintf("%+d%%", ((totals.Micros-prior.Micros)*100)/prior.Micros)
	}

	b := shareBlock{
		Version:      buildinfo.Version,
		Window:       fmt.Sprintf("%dd", r.Days()),
		Vendors:      len(vendors),
		VendorMix:    strings.Join(mix, "/"),
		Models:       len(models),
		Keys:         len(keys),
		Unattributed: unattributedPct,
		Change:       change,
		Surprised:    surprised,
	}

	fmt.Fprintf(w, "\n  %s\n", caps.Dim(
		"Copy the block below into your reply. It contains no amounts, no key"))
	fmt.Fprintf(w, "  %s\n\n", caps.Dim(
		"identifiers, and no project or model names."))

	lines := [][2]string{
		{"", fmt.Sprintf("aispend %s %s %s %s %d vendors connected", b.Version, caps.Sep(), b.Window, caps.Sep(), b.Vendors)},
		{"vendor mix", b.VendorMix},
		{"models in use", fmt.Sprint(b.Models)},
		{"distinct keys", fmt.Sprint(b.Keys)},
		{"unattributed", fmt.Sprintf("%d%%", b.Unattributed)},
		{b.Window + " change", b.Change},
	}
	if b.Surprised != "" {
		lines = append(lines, [2]string{"surprised", b.Surprised})
	}

	width := 0
	for _, l := range lines {
		if n := len(l[0]) + len(l[1]) + 3; n > width {
			width = n
		}
	}
	if width < 56 {
		width = 56
	}

	box := boxChars(caps)
	fmt.Fprintf(w, "  %s%s%s\n", box.tl, strings.Repeat(box.h, width), box.tr)
	for _, l := range lines {
		body := l[1]
		if l[0] != "" {
			body = fmt.Sprintf("%-16s %s", l[0], l[1])
		}
		fmt.Fprintf(w, "  %s %-*s %s\n", box.v, width-2, body, box.v)
	}
	fmt.Fprintf(w, "  %s%s%s\n", box.bl, strings.Repeat(box.h, width), box.br)
	return nil
}

type boxSet struct{ tl, tr, bl, br, h, v string }

func boxChars(caps ui.Caps) boxSet {
	if caps.UTF8 {
		return boxSet{"┌", "┐", "└", "┘", "─", "│"}
	}
	return boxSet{"+", "+", "+", "+", "-", "|"}
}

// askSurprised is the one optional question, and it is literally the metric the
// whole exercise exists to measure. One keystroke, skippable with Enter, and
// silently skipped when there is no terminal — a tool that blocks in CI is a
// tool that gets uninstalled.
func askSurprised(cmd *cobra.Command) string {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return ""
	}
	fmt.Fprint(cmd.OutOrStdout(), "\n  Did that number surprise you? [y/n/skip]: ")

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return "yes"
	case "n", "no":
		return "no"
	default:
		return ""
	}
}
