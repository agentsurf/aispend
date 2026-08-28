package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/prabhuvmk/aispend/internal/buildinfo"
	"github.com/prabhuvmk/aispend/internal/dbg"
)

// userAgent identifies the tool to vendors. Anthropic's docs ask integrations
// to set one; it carries no machine or user identity.
var userAgent = "aispend/" + buildinfo.Version + " (+https://github.com/prabhuvmk/aispend)"

// maxBody caps how much of a response is read. A vendor returning something
// enormous should fail as a bad response, not as memory pressure.
const maxBody = 32 << 20

// fetch performs one authenticated read, with rate limiting and retry.
//
// build is a function rather than a request because a retry needs a fresh one,
// and because the credential is applied at the last moment — inside this
// package, never by a caller holding a header map around.
func fetch(ctx context.Context, client *http.Client, vendorID string, lim *limiter,
	build func() (*http.Request, error), out any) error {

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if lim != nil {
			if err := lim.wait(ctx); err != nil {
				return err
			}
		}

		req, err := build()
		if err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err != nil {
			// A transport failure is not retried here: the guard refusing a
			// host, a bad fixture and a DNS failure are all permanent, and the
			// dialer already applies its own timeout.
			return classify(vendorID, err)
		}

		if !retryable(resp.StatusCode) {
			defer resp.Body.Close()
			return decode(vendorID, resp, out)
		}

		// Retryable: drain and close so the connection can be reused, then wait.
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()

		lastErr = httpError(vendorID, resp.StatusCode, "")
		if attempt == maxAttempts {
			break
		}

		wait := backoff(attempt, resp.Header)
		dbg.Printf("%s %d on attempt %d/%d, retrying in %s",
			vendorID, resp.StatusCode, attempt, maxAttempts, wait.Round(time.Millisecond))
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
	return lastErr
}

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
