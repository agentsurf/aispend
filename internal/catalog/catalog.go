// Package catalog is the single source of truth for what a vendor is: its
// endpoints, its rate limit, the credential it needs, and — critically — the
// hosts the binary is permitted to contact.
//
// The allowed hosts live here rather than in a separate allowlist so the
// security guarantee and the vendor definition cannot drift apart: they are the
// same file (design §3). The egress guard in run 7 reads IsAllowedHost, so
// adding a vendor and permitting its host are one edit, and permitting a host
// that no vendor uses is impossible.
//
// It is compiled in, not stored in SQLite: it is static configuration that
// ships with the version, not user data, so a fresh install has a working
// catalog with no bootstrap step and adding a vendor needs no migration.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed catalog.json
var catalogJSON []byte

// Catalog is the parsed vendor list.
type Catalog struct {
	Version string   `json:"version"`
	Vendors []Vendor `json:"vendors"`
}

// Vendor is everything the binary knows about one provider.
type Vendor struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	UnitKind     string            `json:"unit_kind"`
	AllowedHosts []string          `json:"allowed_hosts"`
	Credential   Credential        `json:"credential"`
	Endpoints    map[string]string `json:"endpoints"`
	RateLimitRPS float64           `json:"rate_limit_rps"`
	// ReportsCost records whether the vendor reports money directly. Where it
	// does, the price book is never consulted and amount_basis stays
	// 'vendor_reported' (design §7.2).
	ReportsCost bool `json:"reports_cost"`
}

// Credential describes the narrowest credential that works, and where to get it.
// The note is what an error message quotes when a key is the wrong type — the
// single most common failure, and the one that wastes the most of a prospect's
// goodwill if handled badly.
type Credential struct {
	Env   []string `json:"env"`
	Kind  string   `json:"kind"`
	Note  string   `json:"note"`
	Where string   `json:"where"`
}

var loaded Catalog

func init() {
	if err := json.Unmarshal(catalogJSON, &loaded); err != nil {
		// The catalog is embedded at build time, so a parse failure here is a
		// broken build, not a runtime condition a user can cause or fix.
		panic(fmt.Sprintf("embedded catalog.json is invalid: %v", err))
	}
	if err := loaded.validate(); err != nil {
		panic(fmt.Sprintf("embedded catalog.json is inconsistent: %v", err))
	}
}

// Load returns the embedded catalog.
func Load() Catalog { return loaded }

// Vendors returns every known vendor, in catalog order.
func Vendors() []Vendor { return loaded.Vendors }

// Get returns one vendor by id.
func Get(id string) (Vendor, bool) {
	for _, v := range loaded.Vendors {
		if v.ID == id {
			return v, true
		}
	}
	return Vendor{}, false
}

// IsAllowedHost reports whether the binary may contact this host. Everything
// else is refused in the dialer, so no code path — not a collector, not a future
// feature, not a mistake — can reach an unlisted host.
func IsAllowedHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, v := range loaded.Vendors {
		for _, h := range v.AllowedHosts {
			if host == strings.ToLower(h) {
				return true
			}
		}
	}
	return false
}

// AllowedHosts returns every permitted host, for --dry-run and the report's
// Privacy footer. Both must be generated from this, never hand-written.
func AllowedHosts() []string {
	var hosts []string
	for _, v := range loaded.Vendors {
		hosts = append(hosts, v.AllowedHosts...)
	}
	return hosts
}

func (c Catalog) validate() error {
	seen := map[string]bool{}
	for _, v := range c.Vendors {
		switch {
		case v.ID == "":
			return fmt.Errorf("vendor with no id")
		case seen[v.ID]:
			return fmt.Errorf("duplicate vendor id %q", v.ID)
		case v.Name == "":
			return fmt.Errorf("%s: no name", v.ID)
		case v.UnitKind == "":
			return fmt.Errorf("%s: no unit_kind", v.ID)
		case len(v.AllowedHosts) == 0:
			return fmt.Errorf("%s: no allowed_hosts — it could never be contacted", v.ID)
		case len(v.Credential.Env) == 0:
			return fmt.Errorf("%s: no credential env vars", v.ID)
		case v.Endpoints["verify"] == "":
			return fmt.Errorf("%s: no verify endpoint", v.ID)
		case v.RateLimitRPS <= 0:
			return fmt.Errorf("%s: rate_limit_rps must be positive", v.ID)
		}
		seen[v.ID] = true
	}
	if len(c.Vendors) == 0 {
		return fmt.Errorf("no vendors")
	}
	return nil
}
