// Package owners reads the optional attribution map.
//
// There is no mapping UI, no import flow and no schema for the user to learn: a
// CSV they drop in, or nothing at all. If it is absent, every fact lands in
// Unattributed — which is displayed loudly, and is itself the pitch. A prospect
// reading "Unattributed $12,024 (78%)" has understood the product without
// anyone saying a word.
package owners

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/dbg"
)

// Unattributed is the bucket for principals with no owner mapping.
const Unattributed = "Unattributed"

// Map resolves a principal to a team.
type Map struct {
	// byPrincipal is keyed "vendor\x1fprincipal_ref".
	byPrincipal map[string]Owner
	// Warnings are malformed rows, named by line, that were skipped. The rest
	// of the file still loads: one bad line must not discard the mapping.
	Warnings []string
	// Loaded reports whether a file was found at all.
	Loaded bool
}

// Owner is who a principal belongs to.
type Owner struct {
	Team       string
	CostCenter string
}

// Load reads owners.csv. A missing file is not an error — it is the normal
// state, and the report it produces is the interesting one.
func Load(path string) (*Map, error) {
	m := &Map{byPrincipal: map[string]Owner{}}

	f, err := os.Open(path)
	if os.IsNotExist(err) {
		dbg.Printf("no owners file at %s — everything will be unattributed", config.Display(path))
		return m, nil
	}
	if err != nil {
		return m, fmt.Errorf("cannot read %s: %w", config.Display(path), err)
	}
	defer f.Close()

	m.Loaded = true
	return m, m.parse(f, config.Display(path))
}

func (m *Map) parse(r io.Reader, name string) error {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // rows are validated here, with a better message
	cr.TrimLeadingSpace = true
	cr.Comment = '#'

	line := 0
	for {
		line++
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			m.Warnings = append(m.Warnings, fmt.Sprintf("%s:%d: %v", name, line, err))
			continue
		}
		if len(rec) == 0 || (len(rec) == 1 && strings.TrimSpace(rec[0]) == "") {
			continue
		}

		// A header row is what a spreadsheet writes out, so accept and skip it
		// rather than reporting it as the file's first error.
		if line == 1 && strings.EqualFold(strings.TrimSpace(rec[0]), "vendor") {
			continue
		}

		if len(rec) < 3 {
			m.Warnings = append(m.Warnings, fmt.Sprintf(
				"%s:%d: expected vendor,principal_ref,team[,cost_center] — got %d field(s)",
				name, line, len(rec)))
			continue
		}

		vendor := strings.TrimSpace(rec[0])
		principal := strings.TrimSpace(rec[1])
		team := strings.TrimSpace(rec[2])
		if vendor == "" || principal == "" || team == "" {
			m.Warnings = append(m.Warnings, fmt.Sprintf(
				"%s:%d: vendor, principal_ref and team are all required", name, line))
			continue
		}

		owner := Owner{Team: team}
		if len(rec) > 3 {
			owner.CostCenter = strings.TrimSpace(rec[3])
		}
		m.byPrincipal[key(vendor, principal)] = owner
	}

	dbg.Printf("owners: %d mappings, %d warnings", len(m.byPrincipal), len(m.Warnings))
	return nil
}

// Team returns the team for a principal, or Unattributed.
//
// Matching is exact on the vendor's own identifier, then falls back to matching
// any vendor. A team named in one vendor's row does not silently claim the same
// key id at another vendor.
func (m *Map) Team(vendor, principal string) string {
	if principal == "" {
		return Unattributed
	}
	if o, ok := m.byPrincipal[key(vendor, principal)]; ok {
		return o.Team
	}
	if o, ok := m.byPrincipal[key("*", principal)]; ok {
		return o.Team
	}
	return Unattributed
}

// Len is how many mappings were loaded.
func (m *Map) Len() int { return len(m.byPrincipal) }

func key(vendor, principal string) string {
	return strings.ToLower(vendor) + "\x1f" + principal
}
