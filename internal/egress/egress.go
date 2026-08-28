// Package egress builds the only HTTP client this binary is allowed to use.
//
// The allowlist is enforced in the dialer, not in a policy a caller has to
// remember: no code path — not a collector, not a future feature, not a mistake
// — can open a connection to a host the catalog does not name. That is the
// sentence you can put in a security questionnaire, and it is the reason
// `aispend scan --dry-run` can honestly print every host the binary could reach.
package egress

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prabhuvmk/aispend/internal/catalog"
	"github.com/prabhuvmk/aispend/internal/dbg"
)

// BlockedError is returned when something tries to contact a host outside the
// catalog. It names the host, because the only useful reaction is to look at
// why that host was reached for.
type BlockedError struct {
	Host string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("blocked egress to %s (not in the vendor catalog)", e.Host)
}

// AllowFunc decides whether a host may be contacted.
type AllowFunc func(host string) bool

// contacted records which hosts this process actually reached.
//
// The report's Privacy line is generated from this rather than from the
// allowlist, because "hosts we are permitted to contact" and "hosts we
// contacted" are different claims and only the second one is what the reader is
// being told. An offline command must be able to say it used no network at all
// — and with fixture mode that statement is verifiable by unplugging the
// machine.
var contacted = struct {
	sync.Mutex
	hosts map[string]bool
}{hosts: map[string]bool{}}

// Contacted returns the hosts reached so far, sorted.
func Contacted() []string {
	contacted.Lock()
	defer contacted.Unlock()

	out := make([]string, 0, len(contacted.hosts))
	for h := range contacted.hosts {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// ResetContacted clears the record. Tests use it; nothing else should.
func ResetContacted() {
	contacted.Lock()
	defer contacted.Unlock()
	contacted.hosts = map[string]bool{}
}

func recordContact(host string) {
	contacted.Lock()
	defer contacted.Unlock()
	contacted.hosts[host] = true
}

// New returns an HTTP client that can only reach catalog hosts.
//
// The check happens twice, on purpose:
//
//   - In the dialer, which is the real guarantee — nothing gets a socket.
//   - In the round tripper, on the request URL, because with an HTTP proxy
//     configured the dialer only ever sees the *proxy's* address. Without the
//     second check, HTTPS_PROXY would be enough to make the allowlist decorative
//     on someone else's machine.
func New(allow AllowFunc) *http.Client {
	if allow == nil {
		allow = catalog.IsAllowedHost
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

	base := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			if !allow(host) {
				dbg.Printf("dialer refused %s", host)
				return nil, &BlockedError{Host: host}
			}
			return dialer.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   2,
	}

	return &http.Client{
		Transport: &guard{base: base, allow: allow},
		Timeout:   60 * time.Second,
		// A redirect to an unlisted host must fail rather than be followed. The
		// dialer would refuse it anyway; failing here produces the clearer error.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !allow(req.URL.Hostname()) {
				return &BlockedError{Host: req.URL.Hostname()}
			}
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			return nil
		},
	}
}

// guard checks the request URL before the transport gets a chance to route it
// through a proxy.
type guard struct {
	base  http.RoundTripper
	allow AllowFunc
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if !g.allow(host) {
		dbg.Printf("transport refused %s %s", req.Method, req.URL.Redacted())
		return nil, &BlockedError{Host: host}
	}
	if req.URL.Scheme != "https" {
		// Credentials travel on these requests. There is no vendor worth
		// reaching over plaintext, so this is a bug rather than a policy.
		return nil, fmt.Errorf("refusing to send %s over %s: only https is allowed",
			req.URL.Path, req.URL.Scheme)
	}
	dbg.Printf("%s %s", req.Method, req.URL.Redacted())
	recordContact(host)
	return g.base.RoundTrip(req)
}

// AllowOnly builds an AllowFunc for an explicit set of hosts. Used by tests and
// by the fixture transport, so neither has to reach for the real catalog.
func AllowOnly(hosts ...string) AllowFunc {
	set := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		set[strings.ToLower(h)] = true
	}
	return func(host string) bool {
		return set[strings.ToLower(strings.TrimSuffix(host, "."))]
	}
}
