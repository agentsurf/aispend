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

type anthropic struct {
	http    *http.Client
	limiter *limiter
}

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

// anthropicBucket is one time bucket of the messages usage report.
type anthropicBucket struct {
	StartingAt string `json:"starting_at"` // RFC 3339, UTC
	EndingAt   string `json:"ending_at"`
	Results    []struct {
		UncachedInputTokens  int64 `json:"uncached_input_tokens"`
		CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
		CacheCreation        struct {
			Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
			Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
		} `json:"cache_creation"`
		OutputTokens  int64 `json:"output_tokens"`
		ServerToolUse struct {
			WebSearchRequests int64 `json:"web_search_requests"`
		} `json:"server_tool_use"`
		// Every dimension is nullable: null means "not grouped by this", and
		// for workspace_id it also means the default workspace, which has no id
		// of its own.
		Model       *string `json:"model"`
		APIKeyID    *string `json:"api_key_id"`
		WorkspaceID *string `json:"workspace_id"`
		ServiceTier *string `json:"service_tier"`
	} `json:"results"`
}

// anthropicMaxBuckets is the most 1d buckets the usage report will return in one
// response. The *default* is 7, which would silently truncate a 30-day scan into
// a week, so limit is always sent explicitly.
const anthropicMaxBuckets = 31

// Collect fetches the messages usage report and streams each row to the emitter.
//
// Like OpenAI, this reports tokens rather than money: Anthropic's cost report
// cannot break spend below workspace level, so attributing it to a model is
// allocation, which arrives with the price book. Facts carry basis 'unknown'
// until then.
func (a *anthropic) Collect(ctx context.Context, c cred.Credential, r timerange.Range,
	cursor string, out Emitter) (string, error) {

	base, err := endpoint("anthropic", "usage")
	if err != nil {
		return cursor, err
	}

	limit := r.Days()
	if limit > anthropicMaxBuckets {
		limit = anthropicMaxBuckets
	}

	collected := time.Now().UTC()
	page := cursor

	for {
		q := url.Values{}
		// Anthropic takes RFC 3339 where OpenAI takes unix seconds. Each
		// collector converts at its own boundary so timerange stays the single
		// place that decides what a UTC day is.
		q.Set("starting_at", r.From.Format(time.RFC3339))
		q.Set("ending_at", r.To.AddDate(0, 0, 1).Format(time.RFC3339))
		q.Set("bucket_width", "1d")
		q.Set("limit", strconv.Itoa(limit))
		for _, g := range []string{"workspace_id", "model", "api_key_id"} {
			q.Add("group_by", g)
		}
		if page != "" {
			q.Set("page", page)
		}

		var body struct {
			Data     []anthropicBucket `json:"data"`
			HasMore  bool              `json:"has_more"`
			NextPage *string           `json:"next_page"`
		}
		if err := a.get(ctx, c, base+"?"+q.Encode(), &body); err != nil {
			return page, err
		}

		for _, bucket := range body.Data {
			start, err := time.Parse(time.RFC3339, bucket.StartingAt)
			if err != nil {
				return page, fmt.Errorf("anthropic returned an unparseable bucket start %q: %w",
					bucket.StartingAt, err)
			}
			day := start.UTC().Format("2006-01-02")

			if day < r.FromDay() || day > r.ToDay() {
				dbg.Printf("anthropic: dropping bucket %s, outside %s..%s", day, r.FromDay(), r.ToDay())
				continue
			}

			for _, row := range bucket.Results {
				cacheWrite := row.CacheCreation.Ephemeral1hInputTokens +
					row.CacheCreation.Ephemeral5mInputTokens

				f := fact.Fact{
					Vendor:       "anthropic",
					Day:          day,
					WorkspaceRef: deref(row.WorkspaceID),
					PrincipalRef: deref(row.APIKeyID),
					ModelRef:     deref(row.Model),
					InputUnits:   row.UncachedInputTokens,
					OutputUnits:  row.OutputTokens,
					// Reads are discounted, writes carry a premium. Kept apart
					// because combining them moves the number in opposite
					// directions and the errors do not cancel.
					CachedUnits:     row.CacheReadInputTokens,
					CacheWriteUnits: cacheWrite,
					// Server-side tool calls are billed per request rather than
					// per token. Counting them here keeps them visible; pricing
					// them is the price book's job.
					OtherUnits:   row.ServerToolUse.WebSearchRequests,
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

		next := deref(body.NextPage)
		if err := out.PageDone(next); err != nil {
			return page, err
		}
		if !body.HasMore || next == "" || next == page {
			return next, nil
		}
		page = next
	}
}

// deref turns a nullable vendor field into the empty string.
//
// Null means "not grouped by this dimension", and for workspace_id it also
// means the default workspace, which has no id. Empty string is how the schema
// records an unreported dimension, and the renderer shows it as an em dash.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// get performs one authenticated read, through the shared retry and rate-limit
// path so no collector has to remember either.
func (a *anthropic) get(ctx context.Context, c cred.Credential, url string, out any) error {
	return fetch(ctx, a.http, "anthropic", a.limiter, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-api-key", c.Secret())
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("User-Agent", userAgent)
		return req, nil
	}, out)
}
