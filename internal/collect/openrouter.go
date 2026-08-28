package collect

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/prabhuvmk/aispend/internal/cred"
	"github.com/prabhuvmk/aispend/internal/dbg"
	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/timerange"
)

type openRouter struct {
	http    *http.Client
	limiter *limiter
}

func (o *openRouter) Vendor() string { return "openrouter" }

// Verify reads the key's own metadata — the cheapest call OpenRouter offers,
// and the only one that works with a plain API key rather than an admin one.
func (o *openRouter) Verify(ctx context.Context, c cred.Credential) (AccountInfo, error) {
	url, err := endpoint("openrouter", "verify")
	if err != nil {
		return AccountInfo{}, err
	}

	var body struct {
		Data struct {
			Label      string   `json:"label"`
			Usage      float64  `json:"usage"`
			Limit      *float64 `json:"limit"`
			IsFreeTier bool     `json:"is_free_tier"`
		} `json:"data"`
	}
	if err := o.get(ctx, c, url, &body); err != nil {
		return AccountInfo{}, err
	}

	info := AccountInfo{Label: body.Data.Label, Details: []string{"1 account"}}
	if body.Data.Label != "" {
		info.AccountRef = body.Data.Label
		info.Details = []string{"key " + body.Data.Label}
	}
	return info, nil
}

// activityRow is one day of OpenRouter's activity report.
//
// OpenRouter is the only v1 vendor that reports money per model directly, so
// its facts carry basis 'vendor_reported' and the price book is never consulted
// for them. That makes it the cheapest connector to write and the best check on
// the other two: where its own figure and a computed one disagree, the price
// book is wrong.
type activityRow struct {
	Date             string  `json:"date"` // YYYY-MM-DD
	Model            string  `json:"model"`
	ModelPermaslug   string  `json:"model_permaslug"`
	Usage            float64 `json:"usage"` // USD
	Requests         float64 `json:"requests"`
	PromptTokens     float64 `json:"prompt_tokens"`
	CompletionTokens float64 `json:"completion_tokens"`
	ReasoningTokens  float64 `json:"reasoning_tokens"`
	APIKeyID         string  `json:"api_key_id"`
}

func (o *openRouter) Collect(ctx context.Context, c cred.Credential, r timerange.Range,
	cursor string, out Emitter) (string, error) {

	base, err := endpoint("openrouter", "usage")
	if err != nil {
		return cursor, err
	}

	q := url.Values{}
	q.Set("date", r.FromDay())

	var body struct {
		Data []activityRow `json:"data"`
	}
	if err := o.get(ctx, c, base+"?"+q.Encode(), &body); err != nil {
		return cursor, err
	}

	collected := time.Now().UTC()
	for _, row := range body.Data {
		day := row.Date
		if len(day) > 10 {
			day = day[:10]
		}
		if day < r.FromDay() || day > r.ToDay() {
			dbg.Printf("openrouter: dropping %s, outside %s..%s", day, r.FromDay(), r.ToDay())
			continue
		}

		model := row.Model
		if model == "" {
			model = row.ModelPermaslug
		}

		f := fact.Fact{
			Vendor:       "openrouter",
			Day:          day,
			PrincipalRef: row.APIKeyID,
			ModelRef:     model,
			InputUnits:   int64(row.PromptTokens),
			OutputUnits:  int64(row.CompletionTokens + row.ReasoningTokens),
			UnitKind:     "token",
			// usage is USD as a JSON number. Converting through micros with a
			// rounded multiply keeps the whole downstream path in integers;
			// nothing after this point sees a float.
			AmountMicros: usdToMicros(row.Usage),
			AmountBasis:  fact.BasisVendorReported,
			Revision:     1,
			CollectedAt:  collected,
		}
		if err := out.Emit(f); err != nil {
			return cursor, err
		}
	}

	// The activity report is a single response per request; there is no cursor
	// to carry forward.
	if err := out.PageDone(""); err != nil {
		return cursor, err
	}
	return "", nil
}

// usdToMicros converts a dollar figure to integer micros, rounding half away
// from zero. This is the only place a float touches money, and it ends here.
func usdToMicros(usd float64) int64 {
	if usd >= 0 {
		return int64(usd*1e6 + 0.5)
	}
	return -int64(-usd*1e6 + 0.5)
}

func (o *openRouter) get(ctx context.Context, c cred.Credential, url string, out any) error {
	return fetch(ctx, o.http, "openrouter", o.limiter, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Secret())
		req.Header.Set("User-Agent", userAgent)
		return req, nil
	}, out)
}
