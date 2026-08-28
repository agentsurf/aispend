package cred

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/prabhuvmk/aispend/internal/catalog"
)

func openai(t *testing.T) catalog.Vendor {
	t.Helper()
	v, ok := catalog.Get("openai")
	if !ok {
		t.Fatal("openai missing from the catalog")
	}
	return v
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, v := range catalog.Vendors() {
		for _, name := range v.Credential.Env {
			t.Setenv(name, "")
		}
	}
}

func TestResolveFindsAnEnvironmentKey(t *testing.T) {
	clearEnv(t)
	t.Setenv("OPENAI_ADMIN_KEY", secret)

	c := Resolve(openai(t))
	if c.Empty() {
		t.Fatal("no credential found")
	}
	if c.Source != SourceEnv {
		t.Errorf("Source = %q, want env", c.Source)
	}
	if c.Ref != "OPENAI_ADMIN_KEY" {
		t.Errorf("Ref = %q — the report must name which variable it used", c.Ref)
	}
	if c.Secret() != secret {
		t.Error("wrong secret")
	}
}

// The catalog lists variables in preference order — the admin key first,
// because it is the one that can actually read organisation usage.
func TestResolvePrefersTheFirstListedVariable(t *testing.T) {
	clearEnv(t)
	t.Setenv("OPENAI_ADMIN_KEY", "sk-admin-000000000000aaaa")
	t.Setenv("OPENAI_API_KEY", "sk-plain-000000000000bbbb")

	if c := Resolve(openai(t)); c.Ref != "OPENAI_ADMIN_KEY" {
		t.Errorf("Ref = %q, want the first listed variable", c.Ref)
	}
}

func TestResolveFallsThroughToTheSecondVariable(t *testing.T) {
	clearEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-plain-000000000000bbbb")

	if c := Resolve(openai(t)); c.Ref != "OPENAI_API_KEY" {
		t.Errorf("Ref = %q, want the fallback variable", c.Ref)
	}
}

// A missing credential is a state to report, not a failure. Most vendors are
// unconfigured on most machines.
func TestResolveWithNoKeyIsNotAnError(t *testing.T) {
	clearEnv(t)

	c := Resolve(openai(t))
	if !c.Empty() {
		t.Errorf("found a credential where none was set: %v", c)
	}
	if c.Vendor != "openai" {
		t.Errorf("Vendor = %q — an empty credential must still say who it is about", c.Vendor)
	}
}

// An exported-but-empty variable is the shape of `export OPENAI_ADMIN_KEY=` in
// a shell profile, and whitespace is what a copy-paste from a console leaves
// behind. Neither is a credential.
func TestResolveIgnoresBlankAndWhitespaceValues(t *testing.T) {
	for _, value := range []string{"", "   ", "\t\n"} {
		clearEnv(t)
		t.Setenv("OPENAI_ADMIN_KEY", value)
		if c := Resolve(openai(t)); !c.Empty() {
			t.Errorf("value %q was accepted as a credential", value)
		}
	}
}

func TestResolveTrimsSurroundingWhitespace(t *testing.T) {
	clearEnv(t)
	t.Setenv("OPENAI_ADMIN_KEY", "  "+secret+"\n")

	if got := Resolve(openai(t)).Secret(); got != secret {
		t.Errorf("Secret() = %q, want the trimmed key", got)
	}
}

func TestResolveAllCoversTheCatalogInOrder(t *testing.T) {
	clearEnv(t)

	all := ResolveAll()
	if len(all) != len(catalog.Vendors()) {
		t.Fatalf("got %d credentials for %d vendors", len(all), len(catalog.Vendors()))
	}
	for i, v := range catalog.Vendors() {
		if all[i].Vendor != v.ID {
			t.Errorf("position %d = %q, want %q", i, all[i].Vendor, v.ID)
		}
	}
}

func TestKeyringRefIsNotASecret(t *testing.T) {
	ref := KeyringRef("openai")
	if !strings.Contains(ref, "openai") {
		t.Errorf("KeyringRef = %q, want it to name the vendor", ref)
	}
	if strings.Contains(ref, secret) {
		t.Error("keyring reference contains key material")
	}
}

// The design's central decision is that credentials never reach SQLite (§1).
// This asserts it structurally rather than by inspection: the package's entire
// dependency tree is checked, so a future edit that imports the store — or
// anything that could write a file — fails here rather than in review.
func TestPackageCannotPersistASecret(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}

	forbidden := []string{
		"database/sql",
		"github.com/prabhuvmk/aispend/internal/store",
		"github.com/prabhuvmk/aispend/internal/sink",
		"net/http",
	}
	for _, dep := range strings.Split(string(out), "\n") {
		for _, bad := range forbidden {
			if strings.TrimSpace(dep) == bad {
				t.Errorf("cred depends on %s — a credential could now be persisted or transmitted", bad)
			}
		}
	}
}
