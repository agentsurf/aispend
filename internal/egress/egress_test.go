package egress

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/prabhuvmk/aispend/internal/catalog"
)

// This is the security claim, so it is written as carefully as production code.
// If it ever fails, the sentence "aispend is structurally incapable of
// contacting any other host" has stopped being true.
func TestBlocksUnlistedHost(t *testing.T) {
	client := New(catalog.IsAllowedHost)

	for _, url := range []string{
		"https://example.com/",
		"https://telemetry.openai.com/v1/collect", // plausible sibling, still not listed
		"https://api.openai.com.evil.test/v1",     // suffix attack
		"https://localhost:9999/",
		"https://127.0.0.1/",
		"https://169.254.169.254/latest/meta-data/", // cloud metadata endpoint
	} {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("building request for %s: %v", url, err)
		}

		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			t.Errorf("%s was NOT blocked", url)
			continue
		}

		var blocked *BlockedError
		if !errors.As(err, &blocked) {
			t.Errorf("%s failed with %v, want a BlockedError", url, err)
		}
	}
}

func TestAllowsCatalogHost(t *testing.T) {
	for _, host := range catalog.AllowedHosts() {
		if !catalog.IsAllowedHost(host) {
			t.Errorf("catalog host %s is not allowed by its own allowlist", host)
		}
	}

	// The guard admits the request; whether the vendor answers is not this
	// test's business, so stop at the transport boundary.
	var reached string
	client := New(catalog.IsAllowedHost)
	client.Transport = &guard{
		allow: catalog.IsAllowedHost,
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			reached = req.URL.Host
			return &http.Response{StatusCode: 200, Body: http.NoBody, Request: req}, nil
		}),
	}

	req, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/organization/projects", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("catalog host was blocked: %v", err)
	}
	resp.Body.Close()
	if reached != "api.openai.com" {
		t.Errorf("reached %q, want api.openai.com", reached)
	}
}

// With HTTPS_PROXY set, the dialer only ever sees the proxy's address — so a
// dialer-only allowlist would let a proxied request reach any host at all. The
// round tripper checks the request URL for exactly this reason.
func TestBlocksUnlistedHostEvenThroughAProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request reached the proxy: %s", r.URL)
	}))
	defer proxy.Close()

	client := New(catalog.IsAllowedHost)
	client.Transport.(*guard).base.(*http.Transport).Proxy = func(*http.Request) (*url.URL, error) {
		return url.Parse(proxy.URL)
	}

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("a proxied request to an unlisted host was allowed")
	}
}

func TestRefusesPlaintextHTTP(t *testing.T) {
	client := New(AllowOnly("api.openai.com"))
	req, _ := http.NewRequest(http.MethodGet, "http://api.openai.com/v1/x", nil)

	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("plaintext http was allowed; credentials travel on these requests")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error does not explain the scheme requirement: %v", err)
	}
}

// A redirect to an unlisted host must fail, not be followed.
func TestBlocksRedirectToUnlistedHost(t *testing.T) {
	client := New(AllowOnly("api.openai.com"))
	req, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/", nil)
	if err := client.CheckRedirect(mustRequest("https://evil.test/"), []*http.Request{req}); err == nil {
		t.Fatal("a redirect to an unlisted host was permitted")
	}
	if err := client.CheckRedirect(mustRequest("https://api.openai.com/other"), []*http.Request{req}); err != nil {
		t.Fatalf("a redirect within the allowlist was refused: %v", err)
	}
}

func TestDialerRefusesBeforeOpeningASocket(t *testing.T) {
	tr := New(AllowOnly("api.openai.com")).Transport.(*guard).base.(*http.Transport)

	if _, err := tr.DialContext(context.Background(), "tcp", "example.com:443"); err == nil {
		t.Fatal("the dialer opened a connection to an unlisted host")
	} else {
		var blocked *BlockedError
		if !errors.As(err, &blocked) {
			t.Errorf("dialer error = %v, want BlockedError", err)
		}
	}
}

func TestAllowOnlyIsCaseInsensitive(t *testing.T) {
	allow := AllowOnly("api.openai.com")
	for _, h := range []string{"api.openai.com", "API.OpenAI.COM", "api.openai.com."} {
		if !allow(h) {
			t.Errorf("AllowOnly rejected %q", h)
		}
	}
	if allow("evil.test") {
		t.Error("AllowOnly accepted an unlisted host")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mustRequest(u string) *http.Request {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		panic(err)
	}
	return req
}
