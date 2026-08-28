package egress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/dbg"
)

// NewFixture returns a client that answers every request from a directory of
// canned JSON files instead of the network.
//
// This is the single highest-leverage development tool in the build: collectors
// are written against a deterministic response first, so the live call becomes a
// transport swap rather than a leap; the renderer iterates in a tight loop with
// no API calls, no rate limits and no spend; and the test suite gets
// deterministic inputs for free.
//
// It opens no sockets at all — not a local one — so a fixture run with the
// network unplugged behaves identically to one with it connected. That is what
// makes it usable as evidence rather than merely convenient.
func NewFixture(dir string) *http.Client {
	return &http.Client{Transport: &fixtureTransport{dir: dir}}
}

type fixtureTransport struct {
	dir string
}

func (f *fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	name := FixtureName(req.URL.Host, req.URL.Path)
	// FixtureName strips path separators, so a crafted URL cannot escape the
	// fixture directory. Assert it at the join anyway: this is the one place a
	// remote-controlled string becomes a filesystem path.
	if name != filepath.Base(name) {
		return nil, fmt.Errorf("refusing fixture name %q: it is not a plain filename", name)
	}
	path := filepath.Join(f.dir, name)

	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, f.missing(req, name)
	}
	if err != nil {
		return nil, fmt.Errorf("reading fixture %s: %w", config.Display(path), err)
	}

	// Validate here rather than letting a collector fail on a half-parsed
	// response: a hand-edited fixture with a trailing comma should say so, at
	// the file and offset, not surface as a confusing mapping bug.
	if !json.Valid(body) {
		var probe any
		detail := json.Unmarshal(body, &probe)
		return nil, fmt.Errorf("fixture %s is not valid JSON: %w", config.Display(path), detail)
	}

	dbg.Printf("fixture %s → %s (%d bytes)", req.URL.Path, name, len(body))

	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

// missing explains what the transport looked for and what is actually there,
// because "file not found" on a path the user never typed is a bad error.
func (f *fixtureTransport) missing(req *http.Request, name string) error {
	entries, _ := os.ReadDir(f.dir)
	var have []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			have = append(have, e.Name())
		}
	}
	sort.Strings(have)

	msg := fmt.Sprintf("no fixture for %s %s\n\n  expected:  %s\n  in:        %s",
		req.Method, req.URL.Path, name, config.Display(f.dir))
	if len(have) == 0 {
		return fmt.Errorf("%s\n\n  that directory has no .json fixtures in it", msg)
	}
	return fmt.Errorf("%s\n\n  available: %s", msg, strings.Join(have, "\n             "))
}

// FixtureName maps a host and path to a fixture filename. The mapping is
// mechanical and reversible so the files stay findable and hand-editable —
// api.openai.com/v1/organization/projects becomes
// api.openai.com_v1_organization_projects.json.
func FixtureName(host, path string) string {
	s := host + strings.TrimSuffix(path, "/")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.' || r == '_' || r == '-':
			return r
		default:
			return '-'
		}
	}, s)
	return s + ".json"
}
