package collect

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prabhuvmk/aispend/internal/cred"
	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/timerange"
)

type anthropic struct{ http *http.Client }

func (a *anthropic) Vendor() string { return "anthropic" }

// Verify lists the organisation's workspaces. Anthropic's usage and cost
// reports live behind the same Admin API credential, so a workspace listing
// that succeeds means the usage report will too.
func (a *anthropic) Verify(ctx context.Context, c cred.Credential) (AccountInfo, error) {
	url, err := endpoint("anthropic", "verify")
	if err != nil {
		return AccountInfo{}, err
	}

	var body struct {
		Data []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			ArchivedAt string `json:"archived_at"`
		} `json:"data"`
		HasMore bool `json:"has_more"`
	}
	if err := a.get(ctx, c, url, &body); err != nil {
		return AccountInfo{}, err
	}

	active := 0
	for _, w := range body.Data {
		if w.ArchivedAt == "" {
			active++
		}
	}

	// The default workspace is reported with a null id and does not appear in
	// this listing, so an organisation with only a default workspace verifies
	// with zero rows. That is a success, not an empty account.
	info := AccountInfo{Details: []string{plural(active, "workspace", "workspaces") + " (plus the default)"}}
	if body.HasMore {
		info.Details = []string{fmt.Sprintf("%d+ workspaces", active)}
	}
	for _, w := range body.Data {
		if w.ArchivedAt == "" {
			info.AccountRef = w.ID
			break
		}
	}
	return info, nil
}

// Collect lands in run 15. Verify above already works, so a credential can be
// checked today even though nothing is collected from it yet.
func (a *anthropic) Collect(context.Context, cred.Credential, timerange.Range, string, func(fact.Fact) error) (string, error) {
	return "", fmt.Errorf("anthropic: %w", ErrNotImplemented)
}

func (a *anthropic) get(ctx context.Context, c cred.Credential, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// Anthropic's Admin API takes the key in x-api-key, not a bearer token, and
	// requires the version header on every request.
	req.Header.Set("x-api-key", c.Secret())
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("User-Agent", userAgent)

	resp, err := a.http.Do(req)
	if err != nil {
		return classify("anthropic", err)
	}
	defer resp.Body.Close()

	return decode("anthropic", resp, out)
}
