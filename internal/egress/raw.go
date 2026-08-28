package egress

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/dbg"
)

// KeepRaw wraps a client so every response body is also written to dir.
//
// This exists to answer "where did that number come from" without a second
// collection run: the saved file is the exact bytes the vendor returned, so a
// disputed figure can be traced to its source. It is off unless --keep-raw is
// passed, and the directory is 0700 with 0600 files, because a usage report is
// a customer's spend even though it holds no credentials.
func KeepRaw(client *http.Client, dir string) *http.Client {
	wrapped := *client
	wrapped.Transport = &rawRecorder{base: client.Transport, dir: dir}
	return &wrapped
}

type rawRecorder struct {
	base http.RoundTripper
	dir  string
}

func (r *rawRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}

	resp, err := base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}

	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if readErr != nil {
		return resp, nil // the caller will hit the same failure and report it
	}

	if err := r.write(req, body); err != nil {
		// Failing to keep a copy must never fail the collection itself.
		dbg.Printf("could not save raw response: %v", err)
	}
	return resp, nil
}

func (r *rawRecorder) write(req *http.Request, body []byte) error {
	if err := os.MkdirAll(r.dir, config.DirPerm); err != nil {
		return err
	}
	if err := os.Chmod(r.dir, config.DirPerm); err != nil {
		return err
	}

	// The query string can carry filters worth keeping but is too long for a
	// filename, so the name is timestamp + endpoint and the URL goes inside.
	name := fmt.Sprintf("%s_%s", time.Now().UTC().Format("20060102T150405.000"),
		FixtureName(req.URL.Host, req.URL.Path))
	path := filepath.Join(r.dir, name)

	// req.URL.Redacted() strips userinfo; credentials travel in headers, which
	// are not written here at all.
	header := fmt.Sprintf("// %s %s\n", req.Method, req.URL.Redacted())
	return os.WriteFile(path, append([]byte(header), body...), config.FilePerm)
}
