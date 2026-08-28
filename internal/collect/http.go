package collect

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/prabhuvmk/aispend/internal/buildinfo"
	"github.com/prabhuvmk/aispend/internal/dbg"
)

// userAgent identifies the tool to vendors. Anthropic's docs ask integrations
// to set one; it carries no machine or user identity.
var userAgent = "aispend/" + buildinfo.Version + " (+https://github.com/prabhuvmk/aispend)"

// maxBody caps how much of a response is read. A vendor returning something
// enormous should fail as a bad response, not as memory pressure.
const maxBody = 32 << 20

// decode turns a response into either a parsed body or an explained error.
func decode(vendorID string, resp *http.Response, out any) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return classify(vendorID, err)
	}

	if resp.StatusCode != http.StatusOK {
		// The body can contain anything, including a credential echoed back, so
		// it goes to the debug channel and never to the terminal.
		dbg.Printf("%s %d body: %s", vendorID, resp.StatusCode, truncate(string(body), 2000))
		return httpError(vendorID, resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, out); err != nil {
		dbg.Printf("%s body: %s", vendorID, truncate(string(body), 2000))
		return fmt.Errorf("%s returned a response aispend could not parse: %w", vendorID, err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "… (truncated)"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
