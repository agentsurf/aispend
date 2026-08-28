package collect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prabhuvmk/aispend/internal/cred"
)

// counting serves a fixed sequence of statuses and records the attempts.
func counting(t *testing.T, statuses []int, headers map[string]string) (*http.Client, *int32) {
	t.Helper()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&calls, 1)) - 1
		status := statuses[len(statuses)-1]
		if n < len(statuses) {
			status = statuses[n]
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		w.Write([]byte(`{"data":[],"has_more":false}`))
	}))
	t.Cleanup(srv.Close)

	client := srv.Client()
	client.Transport = rewriteHost{base: client.Transport, to: strings.TrimPrefix(srv.URL, "http://")}
	return client, &calls
}

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	client, calls := counting(t, []int{429, 429, 200}, map[string]string{"Retry-After": "0"})

	c := &openAI{http: client, limiter: newLimiter(1000)}
	if _, err := c.Verify(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Errorf("made %d attempts, want 3", got)
	}
}

func TestRetriesOn500(t *testing.T) {
	client, calls := counting(t, []int{500, 503, 200}, nil)

	c := &openAI{http: client, limiter: newLimiter(1000)}
	if _, err := c.Verify(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Errorf("made %d attempts, want 3", got)
	}
}

// The rule that matters most: a 403 is a credential problem. Retrying it just
// delays a clear error behind thirty seconds of spinner, which reads as "slow
// and then broken" rather than "your key is the wrong type".
func TestNeverRetriesA403(t *testing.T) {
	client, calls := counting(t, []int{403}, nil)

	c := &openAI{http: client, limiter: newLimiter(1000)}
	if _, err := c.Verify(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey)); err == nil {
		t.Fatal("403 was not an error")
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("made %d attempts on a 403, want exactly 1", got)
	}
}

func TestNeverRetriesA401Or404(t *testing.T) {
	for _, status := range []int{401, 404, 400} {
		client, calls := counting(t, []int{status}, nil)
		c := &openAI{http: client, limiter: newLimiter(1000)}
		c.Verify(context.Background(), cred.New("openai", cred.SourceEnv, "K", testKey))
		if got := atomic.LoadInt32(calls); got != 1 {
			t.Errorf("status %d: made %d attempts, want 1", status, got)
		}
	}
}

func TestGivesUpAfterMaxAttempts(t *testing.T) {
	client, calls := counting(t, []int{503}, map[string]string{"Retry-After": "0"})

	c := &openAI{http: client, limiter: newLimiter(1000)}
	if _, err := c.Verify(context.Background(),
		cred.New("openai", cred.SourceEnv, "K", testKey)); err == nil {
		t.Fatal("persistent 503 was not an error")
	}
	if got := atomic.LoadInt32(calls); got != maxAttempts {
		t.Errorf("made %d attempts, want %d", got, maxAttempts)
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	header := http.Header{"Retry-After": []string{"2"}}
	if got := backoff(1, header); got != 2*time.Second {
		t.Errorf("backoff with Retry-After: 2 = %s, want 2s", got)
	}

	// A vendor asking for longer than we are willing to wait is capped.
	header.Set("Retry-After", "9999")
	if got := backoff(1, header); got != maxBackoff {
		t.Errorf("an absurd Retry-After was not capped: %s", got)
	}
}

// Jitter keeps several vendors from resynchronising into a thundering herd
// after a shared outage.
func TestBackoffGrowsAndJitters(t *testing.T) {
	if backoff(3, nil) <= backoff(1, nil)/2 {
		t.Error("backoff does not grow with attempts")
	}

	seen := map[time.Duration]bool{}
	for i := 0; i < 20; i++ {
		seen[backoff(3, nil)] = true
	}
	if len(seen) < 2 {
		t.Error("backoff is not jittered")
	}
}

func TestRetryableStatuses(t *testing.T) {
	for _, s := range []int{429, 500, 502, 503, 504} {
		if !retryable(s) {
			t.Errorf("status %d should be retryable", s)
		}
	}
	for _, s := range []int{200, 400, 401, 403, 404, 422} {
		if retryable(s) {
			t.Errorf("status %d must not be retried", s)
		}
	}
}

// Requests within one vendor stay spaced: being greedy on an admin endpoint
// costs a 429 storm on a prospect's account.
func TestLimiterSpacesRequests(t *testing.T) {
	l := newLimiter(50) // 20ms apart
	ctx := context.Background()

	started := time.Now()
	for i := 0; i < 3; i++ {
		if err := l.wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(started); elapsed < 35*time.Millisecond {
		t.Errorf("three requests took %s, want at least ~40ms of spacing", elapsed)
	}
}

func TestLimiterRespectsContextCancellation(t *testing.T) {
	l := newLimiter(1) // one second apart
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	l.wait(ctx) // consume the first, immediate token
	if err := l.wait(ctx); err == nil {
		t.Error("limiter ignored a cancelled context")
	}
}

// One vendor failing must never abort the others: a report covering two of
// three, saying plainly what went wrong with the third, beats no report.
func TestRunIsolatesVendorFailures(t *testing.T) {
	boom := &VendorError{Vendor: "b", What: "403 Forbidden"}
	jobs := []Job{
		{Vendor: "a", Count: func() int { return 7 },
			Run: func(context.Context) (string, error) { return "cursor-a", nil }},
		{Vendor: "b", Count: func() int { return 0 },
			Run: func(context.Context) (string, error) { return "", boom }},
		{Vendor: "c", Count: func() int { return 3 },
			Run: func(context.Context) (string, error) { return "", nil }},
	}

	results := Run(context.Background(), jobs, 4)
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if results[0].Err != nil || results[0].Facts != 7 || results[0].Cursor != "cursor-a" {
		t.Errorf("vendor a: %+v", results[0])
	}
	if results[1].Err == nil {
		t.Error("vendor b's failure was lost")
	}
	if results[2].Err != nil || results[2].Facts != 3 {
		t.Errorf("vendor c was affected by b's failure: %+v", results[2])
	}
}

// Results stay in the order the jobs were given, so output is stable between
// runs regardless of which vendor happens to finish first.
func TestRunPreservesJobOrder(t *testing.T) {
	var jobs []Job
	for _, name := range []string{"openai", "anthropic", "openrouter"} {
		v := name
		delay := time.Duration(len(v)) * time.Millisecond
		jobs = append(jobs, Job{
			Vendor: v, Count: func() int { return 0 },
			Run: func(context.Context) (string, error) { time.Sleep(delay); return "", nil },
		})
	}

	results := Run(context.Background(), jobs, 4)
	for i, want := range []string{"openai", "anthropic", "openrouter"} {
		if results[i].Vendor != want {
			t.Errorf("position %d = %s, want %s", i, results[i].Vendor, want)
		}
	}
}

func TestRunLimitsParallelism(t *testing.T) {
	var running, peak int32

	var jobs []Job
	for i := 0; i < 8; i++ {
		jobs = append(jobs, Job{
			Vendor: "v", Count: func() int { return 0 },
			Run: func(context.Context) (string, error) {
				n := atomic.AddInt32(&running, 1)
				for {
					p := atomic.LoadInt32(&peak)
					if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				atomic.AddInt32(&running, -1)
				return "", nil
			},
		})
	}

	Run(context.Background(), jobs, 2)
	if got := atomic.LoadInt32(&peak); got > 2 {
		t.Errorf("peak parallelism = %d, want at most 2", got)
	}
}
