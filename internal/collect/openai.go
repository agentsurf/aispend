package collect

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prabhuvmk/aispend/internal/cred"
	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/timerange"
)

type openAI struct{ http *http.Client }

func (o *openAI) Vendor() string { return "openai" }

// Verify lists the organisation's projects — the cheapest read that proves both
// that the key works and that it is an admin key rather than a regular one.
func (o *openAI) Verify(ctx context.Context, c cred.Credential) (AccountInfo, error) {
	url, err := endpoint("openai", "verify")
	if err != nil {
		return AccountInfo{}, err
	}

	var body struct {
		Data []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"data"`
		HasMore bool `json:"has_more"`
	}
	if err := o.get(ctx, c, url, &body); err != nil {
		return AccountInfo{}, err
	}

	active := 0
	for _, p := range body.Data {
		if p.Status != "archived" {
			active++
		}
	}

	info := AccountInfo{Details: []string{plural(active, "project", "projects")}}
	if body.HasMore {
		info.Details = []string{fmt.Sprintf("%d+ projects", active)}
	}
	for _, p := range body.Data {
		if p.Status != "archived" && p.Name != "" {
			info.AccountRef = p.ID
			break
		}
	}
	return info, nil
}

// Collect lands in the next run. Returning an explicit error rather than an
// empty result keeps a half-built collector from looking like a vendor with no
// usage — the design's "unknown is not zero" rule applied to the build itself.
func (o *openAI) Collect(context.Context, cred.Credential, timerange.Range, string, func(fact.Fact) error) (string, error) {
	return "", fmt.Errorf("openai: collection is not implemented in this build yet")
}

// get performs one authenticated read and decodes it.
func (o *openAI) get(ctx context.Context, c cred.Credential, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Secret())
	req.Header.Set("User-Agent", userAgent)

	resp, err := o.http.Do(req)
	if err != nil {
		return classify("openai", err)
	}
	defer resp.Body.Close()

	return decode("openai", resp, out)
}
