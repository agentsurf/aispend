package cred

import (
	"os"
	"strings"

	"github.com/prabhuvmk/aispend/internal/catalog"
	"github.com/prabhuvmk/aispend/internal/dbg"
)

// Resolve finds a vendor's credential: environment first, OS keychain second.
//
// Environment first is what makes `scan` a zero-setup command — two exported
// variables and the tool runs, with nothing stored anywhere. The keychain is
// the opt-in persistent path for people who want `sync` to keep working across
// sessions.
//
// A missing credential is not an error. Most vendors are unconfigured on most
// machines, and that is a state to report, not a failure.
func Resolve(v catalog.Vendor) Credential {
	for _, name := range v.Credential.Env {
		if secret := strings.TrimSpace(os.Getenv(name)); secret != "" {
			c := New(v.ID, SourceEnv, name, secret)
			dbg.Printf("%s: %s", v.ID, c) // c.String() redacts
			return c
		}
	}

	if c, ok := fromKeyring(v.ID); ok {
		dbg.Printf("%s: %s", v.ID, c)
		return c
	}

	dbg.Printf("%s: no credential in %s or the keychain", v.ID, strings.Join(v.Credential.Env, ", "))
	return Credential{Vendor: v.ID}
}

// ResolveAll resolves every vendor in the catalog, in catalog order.
func ResolveAll() []Credential {
	vendors := catalog.Vendors()
	out := make([]Credential, 0, len(vendors))
	for _, v := range vendors {
		out = append(out, Resolve(v))
	}
	return out
}

// KeyringRef is the lookup key for a vendor's entry in the OS credential store.
// It is what gets written to the connection table — a reference, never a secret.
func KeyringRef(vendor string) string { return "aispend:" + vendor }
