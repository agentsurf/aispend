package catalog

import "testing"

// The embedded catalog must be valid, because init() panics if it isn't and a
// panic on startup is not a failure mode a user should ever meet.
func TestEmbeddedCatalogLoads(t *testing.T) {
	c := Load()
	if c.Version == "" {
		t.Error("no catalog version")
	}
	if len(c.Vendors) != 3 {
		t.Errorf("got %d vendors, want the 3 v1 vendors", len(c.Vendors))
	}
	for _, want := range []string{"openai", "anthropic", "openrouter"} {
		if _, ok := Get(want); !ok {
			t.Errorf("vendor %q missing from catalog", want)
		}
	}
}

func TestEveryVendorIsComplete(t *testing.T) {
	for _, v := range Vendors() {
		if len(v.AllowedHosts) == 0 {
			t.Errorf("%s: no allowed hosts — it could never be contacted", v.ID)
		}
		if v.Endpoints["verify"] == "" {
			t.Errorf("%s: no verify endpoint — doctor could not check it", v.ID)
		}
		if v.Credential.Kind == "" || v.Credential.Where == "" {
			t.Errorf("%s: credential kind and location are what the error message quotes", v.ID)
		}
		if v.RateLimitRPS <= 0 {
			t.Errorf("%s: rate limit must be positive", v.ID)
		}
	}
}

// This is the test the egress guard depends on in run 7: the allowlist is
// derived from the catalog and contains nothing else.
func TestIsAllowedHost(t *testing.T) {
	allowed := []string{"api.openai.com", "api.anthropic.com", "openrouter.ai"}
	for _, h := range allowed {
		if !IsAllowedHost(h) {
			t.Errorf("IsAllowedHost(%q) = false, want true", h)
		}
	}

	blocked := []string{
		"example.com",
		"telemetry.openai.com",     // a plausible-looking sibling is still not listed
		"api.openai.com.evil.test", // suffix attack
		"evil.test/api.openai.com",
		"",
		"localhost",
		"127.0.0.1",
	}
	for _, h := range blocked {
		if IsAllowedHost(h) {
			t.Errorf("IsAllowedHost(%q) = true, want false", h)
		}
	}
}

// Hostname comparison is case-insensitive and tolerates the trailing dot of a
// fully-qualified name, because a blocked-by-formatting bug looks identical to
// a blocked-by-policy one and would waste an afternoon.
func TestIsAllowedHostNormalises(t *testing.T) {
	for _, h := range []string{"API.OpenAI.com", "api.openai.com."} {
		if !IsAllowedHost(h) {
			t.Errorf("IsAllowedHost(%q) = false, want true", h)
		}
	}
}

func TestAllowedHostsCoversEveryVendor(t *testing.T) {
	hosts := AllowedHosts()
	if len(hosts) < len(Vendors()) {
		t.Errorf("got %d hosts for %d vendors", len(hosts), len(Vendors()))
	}
	for _, h := range hosts {
		if !IsAllowedHost(h) {
			t.Errorf("AllowedHosts returned %q but IsAllowedHost rejects it", h)
		}
	}
}

func TestValidateRejectsIncompleteVendors(t *testing.T) {
	cases := map[string]Catalog{
		"no vendors": {Version: "x"},
		"no id":      {Vendors: []Vendor{{Name: "X", UnitKind: "token"}}},
		"no hosts":   {Vendors: []Vendor{{ID: "x", Name: "X", UnitKind: "token"}}},
		"no rate":    {Vendors: []Vendor{{ID: "x", Name: "X", UnitKind: "token", AllowedHosts: []string{"h"}, Credential: Credential{Env: []string{"E"}}, Endpoints: map[string]string{"verify": "/v"}}}},
		"duplicate id": {Vendors: []Vendor{
			{ID: "x", Name: "X", UnitKind: "token", AllowedHosts: []string{"h"}, Credential: Credential{Env: []string{"E"}}, Endpoints: map[string]string{"verify": "/v"}, RateLimitRPS: 1},
			{ID: "x", Name: "Y", UnitKind: "token", AllowedHosts: []string{"h"}, Credential: Credential{Env: []string{"E"}}, Endpoints: map[string]string{"verify": "/v"}, RateLimitRPS: 1},
		}},
	}
	for name, c := range cases {
		if err := c.validate(); err == nil {
			t.Errorf("%s: validate() = nil, want an error", name)
		}
	}
}
