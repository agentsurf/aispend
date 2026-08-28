package collect

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prabhuvmk/aispend/internal/cred"
	"github.com/prabhuvmk/aispend/internal/egress"
)

const testKey = "sk-test-0000000000000000a4f2"

// serve stands in for a vendor and records the request it received. The
// collectors take an *http.Client, so the same code runs against the live
// guarded client, a fixture directory, or this.
func serve(t *testing.T, status int, body string) (*http.Client, *http.Header) {
	t.Helper()

	seen := &http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	client := srv.Client()
	client.Transport = rewriteHost{base: client.Transport, to: strings.TrimPrefix(srv.URL, "http://")}
	return client, seen
}

type rewriteHost struct {
	base http.RoundTripper
	to   string
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = "http", r.to
	return r.base.RoundTrip(req)
}

func TestOpenAIVerify(t *testing.T) {
	client, sent := serve(t, 200, `{"data":[
		{"id":"proj_a","name":"platform","status":"active"},
		{"id":"proj_b","name":"search","status":"active"},
		{"id":"proj_c","name":"old","status":"archived"}],"has_more":false}`)

	info, err := (&openAI{http: client}).Verify(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// Archived projects are excluded: reporting them would overstate what the
	// key can see.
	if got := strings.Join(info.Details, " "); got != "2 projects" {
		t.Errorf("Details = %q, want \"2 projects\"", got)
	}
	if info.AccountRef != "proj_a" {
		t.Errorf("AccountRef = %q", info.AccountRef)
	}
	if got := sent.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q", got)
	}
}

func TestAnthropicVerify(t *testing.T) {
	client, sent := serve(t, 200, `{"data":[
		{"id":"wrkspc_22","name":"prod","archived_at":null},
		{"id":"wrkspc_31","name":"staging","archived_at":null}],"has_more":false}`)

	info, err := (&anthropic{http: client}).Verify(context.Background(),
		cred.New("anthropic", cred.SourceEnv, "ANTHROPIC_ADMIN_KEY", testKey))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !strings.Contains(strings.Join(info.Details, " "), "2 workspaces") {
		t.Errorf("Details = %v", info.Details)
	}
	// Anthropic takes the key in x-api-key, not a bearer token, and requires
	// the version header on every request.
	if got := sent.Get("x-api-key"); got != testKey {
		t.Errorf("x-api-key = %q", got)
	}
	if got := sent.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want 2023-06-01", got)
	}
	if sent.Get("Authorization") != "" {
		t.Error("Anthropic request carried a bearer token as well as x-api-key")
	}
}

// An organisation whose usage is all in the default workspace verifies with
// zero rows. That is a success, not an empty account — the default workspace is
// reported with a null id and never appears in this listing.
func TestAnthropicVerifyWithOnlyTheDefaultWorkspace(t *testing.T) {
	client, _ := serve(t, 200, `{"data":[],"has_more":false}`)

	info, err := (&anthropic{http: client}).Verify(context.Background(),
		cred.New("anthropic", cred.SourceEnv, "ANTHROPIC_ADMIN_KEY", testKey))
	if err != nil {
		t.Fatalf("an org with only a default workspace failed to verify: %v", err)
	}
	if !strings.Contains(strings.Join(info.Details, " "), "default") {
		t.Errorf("Details = %v, want the default workspace mentioned", info.Details)
	}
}

// A 403 means the key works but is the wrong type. The message must name the
// credential kind and where to get it, because this is the single most common
// failure and the one that wastes the most goodwill if handled badly.
func TestForbiddenExplainsTheKeyType(t *testing.T) {
	client, _ := serve(t, 403, `{"error":{"message":"insufficient permissions"}}`)

	_, err := (&anthropic{http: client}).Verify(context.Background(),
		cred.New("anthropic", cred.SourceEnv, "ANTHROPIC_ADMIN_KEY", testKey))
	if err == nil {
		t.Fatal("403 was not an error")
	}

	var ve *VendorError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %T, want *VendorError", err)
	}
	if !strings.Contains(ve.Why, "Admin") {
		t.Errorf("Why does not name the key type: %q", ve.Why)
	}
	if !strings.Contains(ve.Fix, "Admin keys") {
		t.Errorf("Fix does not say where to get one: %q", ve.Fix)
	}
}

func TestUnauthorizedIsNotConfusedWithForbidden(t *testing.T) {
	client, _ := serve(t, 401, `{"error":"invalid key"}`)

	_, err := (&openAI{http: client}).Verify(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey))

	var ve *VendorError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %T", err)
	}
	if !strings.Contains(ve.Why, "rejected") {
		t.Errorf("401 Why = %q, want it to say the key was rejected", ve.Why)
	}
}

// The vendor's response body may contain anything, including a credential it
// echoed back. It must never reach the user's terminal.
func TestErrorMessageNeverCarriesTheResponseBody(t *testing.T) {
	client, _ := serve(t, 403, `{"error":"key `+testKey+` lacks permission"}`)

	_, err := (&openAI{http: client}).Verify(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey))
	if err == nil {
		t.Fatal("no error")
	}

	var ve *VendorError
	errors.As(err, &ve)
	full := ve.What + " " + ve.Why + " " + ve.Fix + " " + err.Error()
	if strings.Contains(full, testKey) {
		t.Errorf("the error carried the response body, which held a credential:\n%s", full)
	}
}

// Unreachable and rejected are different facts. Collapsing them is how a
// prospect behind a corporate proxy is told their good admin key is bad.
func TestConnectivityFailureIsNotACredentialFailure(t *testing.T) {
	client := egress.New(egress.AllowOnly("nothing.allowed"))

	_, err := (&openAI{http: client}).Verify(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey))
	if err == nil {
		t.Fatal("no error")
	}

	var ve *VendorError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %T", err)
	}
	if !ve.Blocked() {
		t.Errorf("a blocked host was not reported as blocked: %+v", ve)
	}
	for _, forbidden := range []string{"key", "credential was rejected", "Admin key"} {
		if strings.Contains(ve.Why, forbidden) {
			t.Errorf("a connectivity failure blamed the credential: %q", ve.Why)
		}
	}
}

func TestRegistryCoversTheImplementedVendors(t *testing.T) {
	r := New(http.DefaultClient)
	vendors := r.Vendors()

	if len(vendors) < 2 {
		t.Fatalf("registry has %d vendors: %v", len(vendors), vendors)
	}
	// Catalog order, so output is stable between runs.
	if vendors[0] != "openai" || vendors[1] != "anthropic" {
		t.Errorf("vendors = %v, want catalog order", vendors)
	}
	for _, v := range vendors {
		c, ok := r.Get(v)
		if !ok || c.Vendor() != v {
			t.Errorf("registry entry %q is inconsistent", v)
		}
	}
}

// No collector may hard-code a host: the URL comes from the catalog, so the
// allowlist and the request can never disagree.
func TestEndpointsComeFromTheCatalog(t *testing.T) {
	for _, v := range []string{"openai", "anthropic"} {
		url, err := endpoint(v, "verify")
		if err != nil {
			t.Errorf("%s: %v", v, err)
			continue
		}
		if !strings.HasPrefix(url, "https://") {
			t.Errorf("%s verify url is not https: %s", v, url)
		}
	}
	if _, err := endpoint("openai", "nonexistent"); err == nil {
		t.Error("a missing endpoint did not error")
	}
}

// http.Client wraps every failure in *url.Error, which itself satisfies
// net.Error — so a naive type check reports a malformed fixture, a TLS failure
// or an outright bug as "check your firewall", sending the user to debug a
// network that is working fine. classify unwraps first for exactly this reason.
func TestNonNetworkFailureIsNotBlamedOnTheNetwork(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "api.openai.com_v1_organization_projects.json"),
		[]byte(`{"data":[},`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := (&openAI{http: egress.NewFixture(dir)}).Verify(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey))
	if err == nil {
		t.Fatal("a malformed fixture was accepted")
	}

	var ve *VendorError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %T", err)
	}
	for _, wrong := range []string{"firewall", "proxy", "credential has not been tested"} {
		if strings.Contains(ve.Why, wrong) {
			t.Errorf("a JSON parse failure was reported as a network problem: %q", ve.Why)
		}
	}
	// Run 8's contract: name the file.
	if !strings.Contains(ve.Why, "api.openai.com_v1_organization_projects.json") {
		t.Errorf("the error does not name the fixture file: %q", ve.Why)
	}
	if !strings.Contains(ve.Why, "not valid JSON") {
		t.Errorf("the error does not say what is wrong: %q", ve.Why)
	}
}

// A timeout still earns the connectivity explanation — the unwrap must not
// throw away the case it was written to preserve.
func TestTimeoutIsStillReportedAsConnectivity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	client := srv.Client()
	client.Timeout = 10 * time.Millisecond
	client.Transport = rewriteHost{base: client.Transport, to: strings.TrimPrefix(srv.URL, "http://")}

	_, err := (&openAI{http: client}).Verify(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey))
	if err == nil {
		t.Fatal("no error")
	}

	var ve *VendorError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %T", err)
	}
	if !strings.Contains(ve.Why, "timed out") && !strings.Contains(ve.Why, "connection failed") {
		t.Errorf("a timeout was not reported as connectivity: %q", ve.Why)
	}
}
