package owners

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "owners.csv")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A missing file is the normal state, and the report it produces — everything
// unattributed — is the interesting one.
func TestMissingFileIsNotAnError(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "owners.csv"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Loaded {
		t.Error("Loaded is true for a file that does not exist")
	}
	if got := m.Team("openai", "key_1"); got != Unattributed {
		t.Errorf("Team = %q, want %q", got, Unattributed)
	}
}

func TestLoadsMappings(t *testing.T) {
	m, err := Load(write(t, `# a comment
vendor,principal_ref,team,cost_center
openai,key_9f2a,platform,ENG-101
anthropic,apikey_01,search,ENG-104
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Len() != 2 {
		t.Errorf("Len = %d, want 2", m.Len())
	}
	if got := m.Team("openai", "key_9f2a"); got != "platform" {
		t.Errorf("Team = %q, want platform", got)
	}
	if got := m.Team("anthropic", "apikey_01"); got != "search" {
		t.Errorf("Team = %q, want search", got)
	}
	if len(m.Warnings) != 0 {
		t.Errorf("warnings on a clean file: %v", m.Warnings)
	}
}

// A header row is what a spreadsheet writes, and comments and blank lines are
// what a human writes. Neither should be reported as an error.
func TestToleratesHeaderCommentsAndBlanks(t *testing.T) {
	m, err := Load(write(t, "\n# comment\nvendor,principal_ref,team\n\nopenai,key_1,platform\n\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Len() != 1 {
		t.Errorf("Len = %d, want 1", m.Len())
	}
	if len(m.Warnings) != 0 {
		t.Errorf("warnings: %v", m.Warnings)
	}
}

// One bad line must not discard the file: a mapping that silently stops working
// because of a typo three rows down is worse than no mapping at all.
func TestMalformedRowWarnsWithLineNumberAndKeepsTheRest(t *testing.T) {
	m, err := Load(write(t, `openai,key_1,platform
garbage
anthropic,apikey_1,search
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Len() != 2 {
		t.Errorf("Len = %d, want the 2 good rows", m.Len())
	}
	if len(m.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1", m.Warnings)
	}
	if !strings.Contains(m.Warnings[0], ":2:") {
		t.Errorf("the warning does not name the line: %q", m.Warnings[0])
	}
}

func TestRowsMissingRequiredFieldsWarn(t *testing.T) {
	m, _ := Load(write(t, "openai,key_1,\nopenai,,platform\n,key_2,platform\n"))
	if m.Len() != 0 {
		t.Errorf("Len = %d, want 0 — none of those rows is usable", m.Len())
	}
	if len(m.Warnings) != 3 {
		t.Errorf("warnings = %v, want 3", m.Warnings)
	}
}

// A team named against one vendor must not silently claim the same key id at
// another vendor: ids are only unique within a vendor.
func TestMappingsAreScopedToTheirVendor(t *testing.T) {
	m, _ := Load(write(t, "openai,key_1,platform\n"))
	if got := m.Team("anthropic", "key_1"); got != Unattributed {
		t.Errorf("Team(anthropic, key_1) = %q, want %q", got, Unattributed)
	}
}

// A wildcard vendor is how someone maps a key they use across several vendors.
func TestWildcardVendorMatches(t *testing.T) {
	m, _ := Load(write(t, "*,shared_key,platform\n"))
	if got := m.Team("anthropic", "shared_key"); got != "platform" {
		t.Errorf("Team = %q, want platform", got)
	}
}

func TestEmptyPrincipalIsUnattributed(t *testing.T) {
	m, _ := Load(write(t, "openai,key_1,platform\n"))
	if got := m.Team("openai", ""); got != Unattributed {
		t.Errorf("a fact with no principal was attributed to %q", got)
	}
}
