package cred

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const secret = "sk-test-0000000000000000a4f2"

// The whole run rests on this: a credential that reaches any formatting verb —
// a log line, a %v in an error, a panic printing a struct — must print masked.
// %#v is included because it prints unexported fields verbatim unless GoString
// is implemented, which is exactly the leak a panic would produce.
func TestNoFormatVerbLeaksTheSecret(t *testing.T) {
	c := New("openai", SourceEnv, "OPENAI_ADMIN_KEY", secret)

	for _, verb := range []string{"%v", "%s", "%+v", "%#v", "%q"} {
		got := fmt.Sprintf(verb, c)
		if strings.Contains(got, secret) {
			t.Errorf("%s leaked the secret: %s", verb, got)
		}
		if !strings.Contains(got, "sk-…a4f2") {
			t.Errorf("%s = %s, want the masked form", verb, got)
		}
	}

	// Wrapped in a struct and in an error, the two shapes a real leak takes.
	type holder struct{ Cred Credential }
	if got := fmt.Sprintf("%+v", holder{c}); strings.Contains(got, secret) {
		t.Errorf("secret leaked through an enclosing struct: %s", got)
	}
	if got := fmt.Errorf("collect failed: %w", fmt.Errorf("using %v", c)).Error(); strings.Contains(got, secret) {
		t.Errorf("secret leaked through a wrapped error: %s", got)
	}
}

// No output format of this tool may carry key material, so the type refuses to
// serialise its secret at all rather than relying on every call site to omit it.
func TestJSONNeverCarriesTheSecret(t *testing.T) {
	c := New("openai", SourceEnv, "OPENAI_ADMIN_KEY", secret)

	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), secret) {
		t.Fatalf("JSON carried the secret: %s", b)
	}
	if !strings.Contains(string(b), "sk-…a4f2") {
		t.Errorf("JSON has no masked form: %s", b)
	}

	// Also inside a document, which is how it would actually appear.
	b, _ = json.Marshal(map[string]any{"credential": c, "vendor": "openai"})
	if strings.Contains(string(b), secret) {
		t.Errorf("nested JSON carried the secret: %s", b)
	}
}

func TestSecretIsStillReachableDeliberately(t *testing.T) {
	c := New("openai", SourceEnv, "OPENAI_ADMIN_KEY", secret)
	if c.Secret() != secret {
		t.Error("Secret() does not return the key; the collectors could not authenticate")
	}
}

func TestMask(t *testing.T) {
	cases := map[string]string{
		secret:                        "sk-…a4f2",
		"sk-ant-admin-00000000009c01": "sk-…9c01",
		"":                            "",
		// Short enough that a prefix and suffix would reveal most of it. A
		// truncated or malformed key is exactly when someone pastes output into
		// a support thread.
		"short":        "…",
		"sk-abc":       "…",
		"12345678901":  "…",
		"123456789012": "123…9012",
	}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}

// Masking counts runes, not bytes, so a non-ASCII value cannot slice a
// character in half and produce mojibake in the one place a user is reading
// carefully.
func TestMaskHandlesMultibyte(t *testing.T) {
	got := Mask("ключ-очень-длинный-ключ")
	if strings.Contains(got, "�") {
		t.Errorf("Mask produced a broken rune: %q", got)
	}
	if !strings.HasPrefix(got, "клю") {
		t.Errorf("Mask(%q) = %q", "ключ…", got)
	}
}

func TestEmptyCredential(t *testing.T) {
	var c Credential
	if !c.Empty() {
		t.Error("zero credential is not Empty()")
	}
	if got := c.String(); got != "no credential" {
		t.Errorf("String() = %q", got)
	}
	if got := c.Display(); got != "" {
		t.Errorf("Display() = %q, want empty", got)
	}
}
