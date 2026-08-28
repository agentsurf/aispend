package collect

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/prabhuvmk/aispend/internal/dbg"
)

// limiter is a token bucket, one per vendor.
//
// Admin and usage endpoints are slower and more aggressively rate limited than
// inference endpoints, so the default is conservative and the catalog can lower
// it further per vendor. Being polite here costs a few seconds; being greedy
// costs a 429 storm on a prospect's account.
type limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newLimiter(rps float64) *limiter {
	if rps <= 0 {
		rps = 2
	}
	return &limiter{interval: time.Duration(float64(time.Second) / rps)}
}

// wait blocks until the next request is allowed, or the context ends.
func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	if l.next.Before(now) {
		l.next = now
	}
	delay := l.next.Sub(now)
	l.next = l.next.Add(l.interval)
	l.mu.Unlock()

	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

const (
	maxAttempts = 5
	baseBackoff = 500 * time.Millisecond
	maxBackoff  = 30 * time.Second
)

// retryable reports whether a status is worth trying again.
//
// Only 429 and 5xx. Every other 4xx is a credential or permission problem, and
// retrying one just delays a clear error behind thirty seconds of spinner —
// which reads as "the tool is slow and then broken" rather than "your key is
// the wrong type".
func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// backoff computes the wait before attempt n, honouring Retry-After when the
// vendor sent one. Jitter keeps several vendors from resynchronising into a
// thundering herd after a shared outage.
func backoff(attempt int, header http.Header) time.Duration {
	if header != nil {
		if v := header.Get("Retry-After"); v != "" {
			if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
				d := time.Duration(secs) * time.Second
				if d > maxBackoff {
					d = maxBackoff
				}
				return d
			}
			if when, err := http.ParseTime(v); err == nil {
				if d := time.Until(when); d > 0 && d <= maxBackoff {
					return d
				}
			}
		}
	}

	d := baseBackoff << (attempt - 1)
	if d > maxBackoff {
		d = maxBackoff
	}
	return d + time.Duration(rand.Int63n(int64(d/2)+1))
}

// sleep waits, unless the context ends first.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Result is one vendor's outcome from a Run.
type Result struct {
	Vendor string
	Facts  int
	Cursor string
	Err    error
	Took   time.Duration
}

// Run collects from several vendors concurrently.
//
// Vendors run in parallel; requests within one vendor stay sequential, because
// the rate limit is per vendor and nothing is gained by racing against it. One
// vendor failing must never abort the others: a report covering two of three
// vendors, saying plainly what went wrong with the third, is worth far more
// than no report at all.
func Run(ctx context.Context, jobs []Job, maxParallel int) []Result {
	if maxParallel < 1 {
		maxParallel = 4
	}

	results := make([]Result, len(jobs))
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup

	for i, job := range jobs {
		wg.Add(1)
		go func(i int, job Job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			started := time.Now()

			// A panic in one vendor's collector must not take the process down
			// with it: the other vendors' results are still worth having, and
			// a crash report is a worse answer than "anthropic failed, here is
			// openai". main() cannot recover this — a panic in a goroutine is
			// only recoverable inside that goroutine.
			var cursor string
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("%s collector crashed: %v", job.Vendor, r)
						dbg.Printf("%s panicked: %v\n%s", job.Vendor, r, debug.Stack())
					}
				}()
				cursor, err = job.Run(ctx)
			}()
			results[i] = Result{
				Vendor: job.Vendor, Facts: job.Count(), Cursor: cursor,
				Err: err, Took: time.Since(started),
			}
			if err != nil && !errors.Is(err, ErrNotImplemented) {
				dbg.Printf("%s failed after %s: %v", job.Vendor, time.Since(started), err)
			}
		}(i, job)
	}

	wg.Wait()
	return results
}

// Job is one vendor's unit of work for Run.
type Job struct {
	Vendor string
	Run    func(ctx context.Context) (cursor string, err error)
	Count  func() int
}
