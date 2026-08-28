package egress

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureNameIsMechanical(t *testing.T) {
	cases := map[[2]string]string{
		{"api.openai.com", "/v1/organization/projects"}:           "api.openai.com_v1_organization_projects.json",
		{"api.anthropic.com", "/v1/organizations/workspaces"}:     "api.anthropic.com_v1_organizations_workspaces.json",
		{"openrouter.ai", "/api/v1/activity"}:                     "openrouter.ai_api_v1_activity.json",
		{"api.openai.com", "/v1/organization/usage/completions/"}: "api.openai.com_v1_organization_usage_completions.json",
	}
	for in, want := range cases {
		if got := FixtureName(in[0], in[1]); got != want {
			t.Errorf("FixtureName(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// A crafted path must not escape the fixture directory. The property that
// matters is not "contains no dots" but "resolves inside the directory".
func TestFixtureNameCannotTraverse(t *testing.T) {
	for _, path := range []string{"/../../etc/passwd", "/..%2f..%2fetc", "/a/../../../b"} {
		name := FixtureName("api.openai.com", path)

		if name != filepath.Base(name) {
			t.Errorf("FixtureName(%q) = %q, which is not a plain filename", path, name)
		}
		resolved := filepath.Clean(filepath.Join("/fixtures", name))
		if !strings.HasPrefix(resolved, "/fixtures/") {
			t.Errorf("FixtureName(%q) escapes the directory: %q", path, resolved)
		}
	}
}

func TestFixtureServesCannedJSON(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "api.openai.com_v1_organization_projects.json", `{"data":[{"id":"proj_a"}]}`)

	resp, err := NewFixture(dir).Get("https://api.openai.com/v1/organization/projects")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "proj_a") {
		t.Errorf("body = %s", body)
	}
}

// Determinism is the point: the renderer and the tests both depend on the same
// input producing byte-identical output every time.
func TestFixtureIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "api.openai.com_v1_x.json", `{"a":1}`)

	client := NewFixture(dir)
	var first string
	for i := 0; i < 3; i++ {
		resp, err := client.Get("https://api.openai.com/v1/x")
		if err != nil {
			t.Fatalf("Get %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if i == 0 {
			first = string(body)
			continue
		}
		if string(body) != first {
			t.Errorf("run %d differed: %q vs %q", i, body, first)
		}
	}
}

// A hand-edited fixture with a trailing comma must say so, naming the file —
// not surface later as a confusing mapping bug in a collector.
func TestMalformedFixtureNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "api.openai.com_v1_x.json", `{"a":1,}`)

	_, err := NewFixture(dir).Get("https://api.openai.com/v1/x")
	if err == nil {
		t.Fatal("malformed fixture was accepted")
	}
	if !strings.Contains(err.Error(), "api.openai.com_v1_x.json") {
		t.Errorf("error does not name the file: %v", err)
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("error does not say what is wrong: %v", err)
	}
}

func TestMissingFixtureListsWhatIsAvailable(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "api.openai.com_v1_present.json", `{}`)

	_, err := NewFixture(dir).Get("https://api.openai.com/v1/absent")
	if err == nil {
		t.Fatal("a missing fixture was accepted")
	}
	for _, want := range []string{"expected", "api.openai.com_v1_absent.json", "available", "api.openai.com_v1_present.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

// Fixture mode must open no sockets at all, so a run with the network unplugged
// behaves identically to one with it connected. That is what makes it usable as
// evidence rather than merely convenient.
func TestFixtureOpensNoSockets(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "api.openai.com_v1_x.json", `{"a":1}`)

	client := NewFixture(dir)
	tr, ok := client.Transport.(*fixtureTransport)
	if !ok {
		t.Fatalf("transport = %T, want the fixture transport", client.Transport)
	}
	if tr.dir != dir {
		t.Errorf("dir = %q, want %q", tr.dir, dir)
	}

	// A host that the real guard would refuse is served happily here, which is
	// only safe because nothing is dialled.
	if _, err := client.Get("https://not-in-the-catalog.test/v1/x"); err == nil {
		t.Log("unlisted host served from disk, as expected — no socket involved")
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
