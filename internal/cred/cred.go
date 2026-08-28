// Package cred resolves API credentials. It is the only place in aispend that
// holds a secret in memory, and it deliberately has no database handle, no
// config writer and no file access: the type cannot persist a credential
// because it was never given anywhere to put one.
//
// That is the design's central decision (§1) made structural. Credentials come
// from the environment or the OS keychain; collected usage goes to SQLite. The
// two never meet, and "never written to the database" is a property of the code
// rather than a rule someone has to remember.
package cred

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Source is where a credential came from.
type Source string

const (
	// SourceEnv — an environment variable. The default, and the one that makes
	// `scan` a zero-setup command.
	SourceEnv Source = "env"
	// SourceKeyring — the OS credential store, encrypted at rest by the
	// operating system. Populated by `connect` in a later run.
	SourceKeyring Source = "keyring"
)

// Credential is one vendor's key.
//
// The secret is unexported and there is no field tag that would expose it: the
// String, GoString and MarshalJSON methods below all redact, so a credential
// that reaches a log line, a %v, a %#v, a panic or a JSON encoder prints its
// masked form. Reading the real value requires calling Secret() explicitly,
// which is greppable and reviewable.
type Credential struct {
	Vendor string
	Source Source
	// Ref names where it came from: the environment variable, or the keychain
	// entry. Never the secret itself.
	Ref string

	secret string
}

// New builds a credential. The secret is never logged here or anywhere else.
func New(vendor string, source Source, ref, secret string) Credential {
	return Credential{Vendor: vendor, Source: source, Ref: ref, secret: secret}
}

// Secret returns the real key. Every call site is a place a secret could leak,
// so this is deliberately the only way to get one and deliberately easy to grep
// for.
func (c Credential) Secret() string { return c.secret }

// Empty reports whether this is the zero credential — no key found.
func (c Credential) Empty() bool { return c.secret == "" }

// Display is the masked form shown to humans: sk-…a4f2.
func (c Credential) Display() string { return Mask(c.secret) }

// String redacts. This is what stops a credential leaking through %v, a log
// line, or a panic that formats a struct containing one.
func (c Credential) String() string {
	if c.Empty() {
		return "no credential"
	}
	return fmt.Sprintf("%s credential from %s %s (%s)", c.Vendor, c.Source, c.Ref, c.Display())
}

// GoString redacts %#v, which would otherwise print unexported fields verbatim.
func (c Credential) GoString() string { return c.String() }

// MarshalJSON redacts. No output format of this tool may carry key material,
// including --json, so the type refuses to serialise its secret at all.
func (c Credential) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Vendor string `json:"vendor"`
		Source Source `json:"source"`
		Ref    string `json:"ref"`
		Masked string `json:"masked"`
	}{c.Vendor, c.Source, c.Ref, c.Display()})
}

// Mask truncates a secret for display: the first few characters, an ellipsis,
// and the last four — enough to tell two keys apart and to match against a
// vendor console, not enough to be a credential.
//
// Anything short enough that a prefix and suffix would reveal most of it is
// masked entirely. A malformed or truncated key is exactly the case where a
// user is most likely to paste output into a support thread.
func Mask(secret string) string {
	const (
		prefix = 3
		suffix = 4
		// Below this, prefix+suffix would expose most of the string.
		minLength = 12
	)

	switch n := utf8.RuneCountInString(secret); {
	case n == 0:
		return ""
	case n < minLength:
		return "…"
	default:
		r := []rune(secret)
		return string(r[:prefix]) + "…" + string(r[n-suffix:])
	}
}
