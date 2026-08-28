package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/sink"
	"github.com/prabhuvmk/aispend/internal/store"
	"github.com/prabhuvmk/aispend/internal/ui"
)

// The database records that a connection exists and where to look the
// credential up. It must never hold the secret, and there is deliberately no
// column that could.
func TestConnectionRecordHoldsNoSecret(t *testing.T) {
	const secret = "sk-test-0000000000000000a4f2"

	db, err := store.Open(filepath.Join(t.TempDir(), "aispend.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.SaveConnection(store.Connection{
		Vendor: "openai", AccountRef: "org_acme", Label: "Acme",
		CredSource: "keyring", KeyringRef: "aispend:openai",
		ConnectedAt: time.Now().Unix(), LastOKAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}

	conns, err := db.Connections()
	if err != nil {
		t.Fatal(err)
	}
	if len(conns) != 1 {
		t.Fatalf("got %d connections, want 1", len(conns))
	}
	if conns[0].KeyringRef != "aispend:openai" {
		t.Errorf("KeyringRef = %q, want a lookup reference", conns[0].KeyringRef)
	}

	// Scan every column of the row for anything resembling the key.
	rows, err := db.SQL().Query("SELECT * FROM connection")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			if s, ok := v.(string); ok && strings.Contains(s, secret) {
				t.Errorf("column %s holds the credential", cols[i])
			}
		}
	}
}

// Disconnecting must take the sync cursor with the facts. Left behind, the next
// scan would resume from a page whose contents are no longer stored and leave a
// gap nothing would ever report.
func TestDeleteVendorFactsAlsoClearsTheCursor(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aispend.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s := sink.NewSQLite(db.SQL())
	if err := s.Write(context.Background(), []fact.Fact{
		{Vendor: "openai", Day: "2026-08-27", ModelRef: "gpt-5.2", UnitKind: "token",
			AmountBasis: fact.BasisUnknown, Revision: 1, CollectedAt: time.Now().UTC()},
		{Vendor: "anthropic", Day: "2026-08-27", ModelRef: "claude-opus-4-6", UnitKind: "token",
			AmountBasis: fact.BasisUnknown, Revision: 1, CollectedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSyncState(store.SyncState{
		Vendor: "openai", CoveredFrom: "2026-08-01", CoveredTo: "2026-08-27", Cursor: "page_x",
	}); err != nil {
		t.Fatal(err)
	}

	n, err := db.DeleteVendorFacts("openai")
	if err != nil {
		t.Fatalf("DeleteVendorFacts: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted %d facts, want 1", n)
	}

	state, _ := db.SyncState("openai")
	if state.Cursor != "" {
		t.Errorf("the resume cursor survived: %q", state.Cursor)
	}

	// The other vendor is untouched.
	if got, _ := db.VendorFactCount("anthropic"); got != 1 {
		t.Errorf("anthropic lost data: %d facts", got)
	}
}

// The share block is shape, not amounts: it is only pasteable because there is
// nothing in it someone would hesitate over.
func TestShareBlockCarriesNoAmountsOrIdentifiers(t *testing.T) {
	db := testDB(t)
	s := sink.NewSQLite(db.SQL())
	at := time.Now().UTC()

	if err := s.Write(context.Background(), []fact.Fact{
		{Vendor: "anthropic", Day: "2026-08-27", WorkspaceRef: "wrkspc_secret_name",
			PrincipalRef: "apikey_01Rj2N8SVvo6BePZj99NhmiT", ModelRef: "claude-opus-4-6",
			UnitKind: "token", AmountMicros: 198_080_000, AmountBasis: fact.BasisComputed,
			Revision: 1, CollectedAt: at},
		{Vendor: "openai", Day: "2026-08-27", WorkspaceRef: "proj_internal",
			PrincipalRef: "key_9f2a", ModelRef: "gpt-5.2", UnitKind: "token",
			AmountMicros: 28_970_000, AmountBasis: fact.BasisComputed,
			Revision: 1, CollectedAt: at},
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := renderShare(&buf, ui.Caps{UTF8: true}, db, testWindow(t), "yes"); err != nil {
		t.Fatalf("renderShare: %v", err)
	}
	got := buf.String()

	for _, leaked := range []string{
		"$", "198", "28.97", "198080000",
		"wrkspc_secret_name", "proj_internal",
		"apikey_01Rj2N8SVvo6BePZj99NhmiT", "key_9f2a",
		"claude-opus-4-6", "gpt-5.2",
	} {
		if strings.Contains(got, leaked) {
			t.Errorf("the share block leaked %q:\n%s", leaked, got)
		}
	}

	// It still has to say something useful.
	for _, want := range []string{"vendor mix", "models in use", "distinct keys", "unattributed", "surprised"} {
		if !strings.Contains(got, want) {
			t.Errorf("the share block is missing %q:\n%s", want, got)
		}
	}
}

// Percentages sorted as text put 9 ahead of 78 — the same class of bug as
// comparing version numbers as strings.
func TestShareVendorMixIsSortedNumerically(t *testing.T) {
	db := testDB(t)
	s := sink.NewSQLite(db.SQL())
	at := time.Now().UTC()

	amounts := map[string]int64{"openai": 9_000_000, "anthropic": 78_000_000, "openrouter": 13_000_000}
	var facts []fact.Fact
	for vendor, micros := range amounts {
		facts = append(facts, fact.Fact{
			Vendor: vendor, Day: "2026-08-27", ModelRef: "m", UnitKind: "token",
			AmountMicros: micros, AmountBasis: fact.BasisComputed,
			Revision: 1, CollectedAt: at})
	}
	if err := s.Write(context.Background(), facts); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := renderShare(&buf, ui.Caps{UTF8: true}, db, testWindow(t), ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "78/13/9") {
		t.Errorf("vendor mix is not sorted numerically descending:\n%s", buf.String())
	}
}

func TestShareOnAnEmptyDatabaseSaysWhatToDo(t *testing.T) {
	db := testDB(t)
	var buf bytes.Buffer
	err := renderShare(&buf, ui.Caps{UTF8: true}, db, testWindow(t), "")
	if err == nil {
		t.Fatal("an empty database produced a share block")
	}
	if !strings.Contains(err.Error(), "aispend scan") {
		t.Errorf("the error does not say what to run: %v", err)
	}
}
