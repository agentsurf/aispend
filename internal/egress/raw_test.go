package egress

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeepRawSavesTheResponse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "raw")
	fixtures := t.TempDir()
	write(t, fixtures, "api.openai.com_v1_x.json", `{"data":[1,2,3]}`)

	resp, err := KeepRaw(NewFixture(fixtures), dir).Get("https://api.openai.com/v1/x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("raw directory not created: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d saved files, want 1", len(entries))
	}

	body, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if !strings.Contains(string(body), `{"data":[1,2,3]}`) {
		t.Errorf("saved file does not hold the response: %s", body)
	}
	if !strings.Contains(string(body), "https://api.openai.com/v1/x") {
		t.Errorf("saved file does not record which request it answers: %s", body)
	}
}

// A usage report is a customer's spend even though it holds no credentials.
func TestKeepRawUsesTightPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "raw")
	fixtures := t.TempDir()
	write(t, fixtures, "api.openai.com_v1_x.json", `{}`)

	resp, _ := KeepRaw(NewFixture(fixtures), dir).Get("https://api.openai.com/v1/x")
	if resp != nil {
		resp.Body.Close()
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory perm = %#o, want 0700", perm)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		fi, _ := e.Info()
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s perm = %#o, want 0600", e.Name(), perm)
		}
	}
}

// The response body must still be readable by the caller after being copied.
func TestKeepRawDoesNotConsumeTheBody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "raw")
	fixtures := t.TempDir()
	write(t, fixtures, "api.openai.com_v1_x.json", `{"ok":true}`)

	client := KeepRaw(NewFixture(fixtures), dir)
	resp, err := client.Get("https://api.openai.com/v1/x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), `"ok":true`) {
		t.Errorf("caller got %q; the recorder consumed the body", buf[:n])
	}
}

// Failing to keep a copy must never fail the collection itself: the recording
// is a convenience, the collection is the job.
func TestKeepRawFailureDoesNotFailTheRequest(t *testing.T) {
	fixtures := t.TempDir()
	write(t, fixtures, "api.openai.com_v1_x.json", `{"ok":true}`)

	// A path that cannot be a directory, because it is a file.
	blocked := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	resp, err := KeepRaw(NewFixture(fixtures), blocked).Get("https://api.openai.com/v1/x")
	if err != nil {
		t.Fatalf("an unwritable raw directory failed the request: %v", err)
	}
	resp.Body.Close()
}
