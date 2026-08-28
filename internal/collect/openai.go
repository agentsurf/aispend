package collect

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/prabhuvmk/aispend/internal/cred"
	"github.com/prabhuvmk/aispend/internal/dbg"
	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/timerange"
)

type openAI struct {
	http    *http.Client
	limiter *limiter
}

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

// usageBucket is one time bucket of the completions usage report.
type usageBucket struct {
	StartTime int64 `json:"start_time"` // unix seconds, UTC
	EndTime   int64 `json:"end_time"`
	Results   []struct {
		InputTokens       int64  `json:"input_tokens"`
		OutputTokens      int64  `json:"output_tokens"`
		InputCachedTokens int64  `json:"input_cached_tokens"`
		InputAudioTokens  int64  `json:"input_audio_tokens"`
		OutputAudioTokens int64  `json:"output_audio_tokens"`
		NumModelRequests  int64  `json:"num_model_requests"`
		ProjectID         string `json:"project_id"`
		APIKeyID          string `json:"api_key_id"`
		UserID            string `json:"user_id"`
		Model             string `json:"model"`
		Batch             *bool  `json:"batch"`
	} `json:"results"`
}

// maxBuckets is the most 1d buckets OpenAI will return in one response.
const maxBuckets = 31

// Collect fetches the completions usage report for the window and streams each
// row to emit.
//
// It reports tokens, not money: OpenAI's usage and cost endpoints are separate,
// and the cost endpoint cannot break spend down to model level. Joining the two
// is allocation, which arrives with the price book. Until then every fact
// carries amount_basis 'unknown' with a zero amount, which the renderer shows
// as an em dash — never as $0.00, which would silently understate the total.
func (o *openAI) Collect(ctx context.Context, c cred.Credential, r timerange.Range,
	cursor string, out Emitter) (string, error) {

	base, err := endpoint("openai", "usage")
	if err != nil {
		return cursor, err
	}

	limit := r.Days()
	if limit > maxBuckets {
		limit = maxBuckets
	}

	collected := time.Now().UTC()
	page := cursor

	for {
		q := url.Values{}
		// OpenAI takes unix seconds; Anthropic takes RFC3339. Each collector
		// converts at its own boundary so timerange stays the one place that
		// decides what a UTC day is.
		q.Set("start_time", strconv.FormatInt(r.From.Unix(), 10))
		q.Set("end_time", strconv.FormatInt(r.To.AddDate(0, 0, 1).Unix(), 10))
		q.Set("bucket_width", "1d")
		q.Set("limit", strconv.Itoa(limit))
		for _, g := range []string{"project_id", "model", "api_key_id"} {
			q.Add("group_by", g)
		}
		if page != "" {
			q.Set("page", page)
		}

		var body struct {
			Data     []usageBucket `json:"data"`
			HasMore  bool          `json:"has_more"`
			NextPage string        `json:"next_page"`
		}
		if err := o.get(ctx, c, base+"?"+q.Encode(), &body); err != nil {
			return page, err
		}

		for _, bucket := range body.Data {
			day := time.Unix(bucket.StartTime, 0).UTC().Format("2006-01-02")

			// The requested window is the contract. A vendor that returns a
			// bucket outside it — an off-by-one at an edge, a wider default
			// than asked for — must not silently add a day the user did not
			// request, because coverage tracking and the prior-window delta
			// both trust that the stored days are the collected days.
			if day < r.FromDay() || day > r.ToDay() {
				dbg.Printf("openai: dropping bucket %s, outside %s..%s", day, r.FromDay(), r.ToDay())
				continue
			}

			for _, row := range bucket.Results {
				f := fact.Fact{
					Vendor:       "openai",
					Day:          day,
					WorkspaceRef: row.ProjectID,
					PrincipalRef: row.APIKeyID,
					ModelRef:     row.Model,
					InputUnits:   row.InputTokens,
					OutputUnits:  row.OutputTokens,
					// Cached tokens are priced at a steep discount, so they stay
					// out of InputUnits. Folding them together produces a number
					// wrong by a margin that grows as the customer optimises.
					CachedUnits: row.InputCachedTokens,
					// Audio tokens are billed on their own rates. They are kept
					// here rather than dropped, because dropping them would
					// understate usage silently.
					OtherUnits:   row.InputAudioTokens + row.OutputAudioTokens,
					UnitKind:     "token",
					AmountMicros: 0,
					AmountBasis:  fact.BasisUnknown,
					Revision:     1,
					CollectedAt:  collected,
				}
				if err := out.Emit(f); err != nil {
					return page, err
				}
			}
		}

		// The page is fully emitted, so it is now safe to resume from here.
		if err := out.PageDone(body.NextPage); err != nil {
			return page, err
		}

		if !body.HasMore || body.NextPage == "" || body.NextPage == page {
			return body.NextPage, nil
		}
		page = body.NextPage
	}
}

// get performs one authenticated read, through the shared retry and rate-limit
// path so no collector has to remember either.
func (o *openAI) get(ctx context.Context, c cred.Credential, url string, out any) error {
	return fetch(ctx, o.http, "openai", o.limiter, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Secret())
		req.Header.Set("User-Agent", userAgent)
		return req, nil
	}, out)
}
