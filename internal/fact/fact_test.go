package fact

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The id must be stable across machines, across runs, and across refactors —
// a server deduping submissions from many agents depends on it, and by the time
// that server exists there are agents in the field that cannot be changed. The
// expected hash is hardcoded so a refactor cannot quietly redefine it.
func TestIDIsDeterministic(t *testing.T) {
	f := Fact{
		Vendor: "openai", Day: "2026-08-27",
		WorkspaceRef: "proj_a91f", PrincipalRef: "sk-test-a4f2", ModelRef: "gpt-5.2",
	}
	// Verified independently, so this pins the documented algorithm rather than
	// whatever the code happens to do:
	//   printf 'openai\x1f2026-08-27\x1fproj_a91f\x1fsk-test-a4f2\x1fgpt-5.2' | shasum -a 256
	const want = "9f3378ef7a07a07681aa788ab5a6687d7d528ab23e898eb0f5cbb4a20b6794e3"
	if got := f.ID(); got != want {
		t.Errorf("ID() = %s\nwant %s\n(if this changed deliberately, every deployed agent's ids change with it)", got, want)
	}
}

// Fields that do not identify the fact must not change its id, or a restatement
// would look like a new fact and the server would double-count it.
func TestIDIgnoresMeasurements(t *testing.T) {
	base := Fact{Vendor: "openai", Day: "2026-08-27", ModelRef: "gpt-5.2"}
	other := base
	other.InputUnits = 999
	other.AmountMicros = 12345
	other.Revision = 7
	other.CollectedAt = time.Now()

	if base.ID() != other.ID() {
		t.Error("id changed when only the measurements changed")
	}
}

func TestIDChangesWithEveryDimension(t *testing.T) {
	base := Fact{Vendor: "openai", Day: "2026-08-27", WorkspaceRef: "w", PrincipalRef: "p", ModelRef: "m"}
	seen := map[string]string{base.ID(): "base"}

	for name, mutate := range map[string]func(*Fact){
		"vendor":    func(f *Fact) { f.Vendor = "anthropic" },
		"day":       func(f *Fact) { f.Day = "2026-08-28" },
		"workspace": func(f *Fact) { f.WorkspaceRef = "w2" },
		"principal": func(f *Fact) { f.PrincipalRef = "p2" },
		"model":     func(f *Fact) { f.ModelRef = "m2" },
	} {
		v := base
		mutate(&v)
		if prev, dup := seen[v.ID()]; dup {
			t.Errorf("changing %s collided with %s", name, prev)
		}
		seen[v.ID()] = name
	}
}

// ("a","bc") and ("ab","c") must not hash alike. A separator that cannot occur
// in a vendor identifier is what guarantees it.
func TestIDHasNoFieldBoundaryCollision(t *testing.T) {
	a := Fact{Vendor: "a", Day: "bc"}
	b := Fact{Vendor: "ab", Day: "c"}
	if a.ID() == b.ID() {
		t.Error("field boundary collision")
	}
}

func TestEnvelopeCarriesVersionAndExactIntegers(t *testing.T) {
	env := Envelope{
		Schema:    EnvelopeSchema,
		Agent:     "0.1.0",
		InstallID: "abc",
		SentAt:    time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
		Facts: []Fact{{
			Vendor: "openai", Day: "2026-08-27", UnitKind: "token",
			InputUnits: 421043882, AmountMicros: 18392400000,
			AmountBasis: BasisVendorReported, Revision: 1,
		}},
	}

	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(b)

	for _, want := range []string{
		`"schema":1`,
		`"agent":"0.1.0"`,
		`"install_id":"abc"`,
		`"sent_at":"2026-08-28T12:00:00Z"`,
		`"input_units":421043882`,     // exact, never humanised
		`"amount_micros":18392400000`, // integer micros, never a float
	} {
		if !strings.Contains(got, want) {
			t.Errorf("envelope missing %s:\n%s", want, got)
		}
	}
	// Every number in the envelope must be an integer. Grepping the text would
	// trip over version strings like "0.1.0", so walk the decoded values.
	for path, n := range jsonNumbers(t, b) {
		if strings.ContainsAny(n.String(), ".eE") {
			t.Errorf("%s = %s is a float; money and units must stay integers", path, n)
		}
	}
}

// jsonNumbers returns every numeric value in the document, keyed by its path.
func jsonNumbers(t *testing.T, b []byte) map[string]json.Number {
	t.Helper()

	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	out := map[string]json.Number{}
	var walk func(string, any)
	walk = func(path string, v any) {
		switch t := v.(type) {
		case json.Number:
			out[path] = t
		case map[string]any:
			for k, child := range t {
				walk(path+"."+k, child)
			}
		case []any:
			for i, child := range t {
				walk(fmt.Sprintf("%s[%d]", path, i), child)
			}
		}
	}
	walk("", doc)
	return out
}

// A server must tolerate fields it doesn't know rather than reject the batch —
// that is what lets agents and server version independently.
func TestEnvelopeUnmarshalTolerantOfUnknownFields(t *testing.T) {
	var env Envelope
	body := `{"schema":1,"agent":"9.9.9","install_id":"x","sent_at":"2026-08-28T12:00:00Z",
	          "facts":[{"vendor":"openai","day":"2026-08-27","future_field":"ignored"}],
	          "another_future_field":123}`
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Schema != 1 || len(env.Facts) != 1 || env.Facts[0].Vendor != "openai" {
		t.Errorf("envelope = %+v", env)
	}
}
