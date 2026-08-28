package redact

import (
	"bytes"
	"strings"
	"testing"
)

// The keys the three v1 vendors actually issue.
var secrets = []string{
	"sk-test-0000000000000000a4f2",
	"sk-proj-abcdefghijklmnopqrstuvwxyz123456",
	"sk-svcacct-abcdefghijklmnopqrstuvwxyz",
	"sk-ant-admin01-abcdefghijklmnopqrstuvwxyz",
	"sk-ant-api03-abcdefghijklmnopqrstuvwxyz",
	"sk-or-v1-abcdefghijklmnopqrstuvwxyz1234",
}

func TestRedactsVendorKeyShapes(t *testing.T) {
	for _, secret := range secrets {
		for _, context := range []string{
			"%s",
			"key %s failed",
			`{"error":"key %s lacks permission"}`,
			"panic: holder{Key:%s}",
			"Authorization: Bearer %s",
			"x-api-key: %s",
		} {
			in := strings.Replace(context, "%s", secret, 1)
			got := String(in)
			if strings.Contains(got, secret) {
				t.Errorf("not redacted in %q: %q", context, got)
			}
			if !strings.Contains(got, Mask) {
				t.Errorf("no mask in %q: %q", context, got)
			}
		}
	}
}

// A header keeps its name so the line still reads sensibly; only the value goes.
func TestHeaderNameSurvives(t *testing.T) {
	got := String("Authorization: Bearer sk-ant-admin01-abcdefghijklmnop")
	if !strings.Contains(got, "Authorization: Bearer") {
		t.Errorf("the header name was destroyed: %q", got)
	}
	if strings.Contains(got, "abcdefghijklmnop") {
		t.Errorf("the token survived: %q", got)
	}
}

// Matching "any long random string" would redact fact ids, cursors and hashes,
// and output full of masks is output nobody reads.
func TestLeavesOrdinaryOutputAlone(t *testing.T) {
	for _, ordinary := range []string{
		"AI SPEND · last 30 days · 29 Jul – 27 Aug 2026",
		"claude-opus-4-6                          $198.08      65%",
		"fact_id 9f3378ef7a07a07681aa788ab5a6687d7d528ab23e898eb0f5cbb4a20b6794e3",
		"page_MjAyNS0wNS0xNFQwMDowMDowMFo=",
		"apikey_01Rj2N8SVvo6BePZj99NhmiT",
		"wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ",
		"proj_a91f",
	} {
		if got := String(ordinary); got != ordinary {
			t.Errorf("redacted ordinary output:\n  in:  %s\n  out: %s", ordinary, got)
		}
	}
}

// A short write would look like an I/O error to the caller and could send it
// into a retry loop.
func TestWriterReportsTheCallersLength(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)

	in := []byte("key sk-ant-admin01-abcdefghijklmnopqrst here")
	n, err := w.Write(in)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(in) {
		t.Errorf("Write reported %d, want the caller's %d", n, len(in))
	}
	if strings.Contains(buf.String(), "abcdefghijklmnopqrst") {
		t.Errorf("the writer let a key through: %q", buf.String())
	}
}

func TestMultipleSecretsInOneLine(t *testing.T) {
	in := "openai sk-proj-aaaaaaaaaaaaaaaaaaaa and anthropic sk-ant-admin01-bbbbbbbbbbbbbbbbbbbb"
	got := String(in)
	for _, part := range []string{"aaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbb"} {
		if strings.Contains(got, part) {
			t.Errorf("one of two secrets survived: %q", got)
		}
	}
}

func TestEmptyAndNilAreSafe(t *testing.T) {
	if got := String(""); got != "" {
		t.Errorf("String(\"\") = %q", got)
	}
	if got := Bytes(nil); len(got) != 0 {
		t.Errorf("Bytes(nil) = %q", got)
	}
}
