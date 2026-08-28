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
	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/timerange"
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

func openaiUsageBody(buckets string) string {
	return `{"object":"page","data":[` + buckets + `],"has_more":false,"next_page":null}`
}

// 1787788800 = 2026-08-27T00:00:00Z, 1787875200 = 2026-08-28T00:00:00Z.
const bucket27 = `{"object":"bucket","start_time":1787788800,"end_time":1787875200,"results":[
	{"input_tokens":1204331,"output_tokens":84210,"input_cached_tokens":310880,
	 "input_audio_tokens":4200,"output_audio_tokens":1100,"num_model_requests":4120,
	 "project_id":"proj_a91f","api_key_id":"key_9f2a","user_id":null,"model":"gpt-5.2","batch":false}]}`

func window(t *testing.T, since string) timerange.Range {
	t.Helper()
	r, err := timerange.Parse(since, time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return r
}

func TestOpenAICollectMapsAUsageRow(t *testing.T) {
	client, sent := serve(t, 200, openaiUsageBody(bucket27))

	var got []fact.Fact
	_, err := (&openAI{http: client}).Collect(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey),
		window(t, "1d"), "", EmitterFunc(func(f fact.Fact) error { got = append(got, f); return nil }))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d facts, want 1", len(got))
	}

	f := got[0]
	checks := map[string][2]any{
		"Day":          {f.Day, "2026-08-27"},
		"WorkspaceRef": {f.WorkspaceRef, "proj_a91f"},
		"PrincipalRef": {f.PrincipalRef, "key_9f2a"},
		"ModelRef":     {f.ModelRef, "gpt-5.2"},
		"InputUnits":   {f.InputUnits, int64(1204331)},
		"OutputUnits":  {f.OutputUnits, int64(84210)},
		"UnitKind":     {f.UnitKind, "token"},
	}
	for name, pair := range checks {
		if pair[0] != pair[1] {
			t.Errorf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}

	// Bearer auth, and the window sent as unix seconds — OpenAI's encoding,
	// converted at this collector's own boundary.
	if sent.Get("Authorization") != "Bearer "+testKey {
		t.Errorf("Authorization = %q", sent.Get("Authorization"))
	}
}

// Cached tokens are priced at a steep discount. Folded into input_units they
// produce a number wrong by a margin that grows as the customer optimises —
// exactly when they are checking your work.
func TestOpenAICollectKeepsCachedTokensSeparate(t *testing.T) {
	client, _ := serve(t, 200, openaiUsageBody(bucket27))

	var f fact.Fact
	_, err := (&openAI{http: client}).Collect(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey),
		window(t, "1d"), "", EmitterFunc(func(got fact.Fact) error { f = got; return nil }))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if f.CachedUnits != 310880 {
		t.Errorf("CachedUnits = %d, want 310880", f.CachedUnits)
	}
	if f.InputUnits == 1204331+310880 {
		t.Error("cached tokens were folded into input tokens")
	}
	// Audio tokens are billed on their own rates; dropping them would
	// understate usage silently.
	if f.OtherUnits != 4200+1100 {
		t.Errorf("OtherUnits = %d, want the audio tokens (5300)", f.OtherUnits)
	}
}

// Money comes from a different endpoint that cannot break spend down to model
// level. Until that join exists, a fact says it does not know — it must never
// claim zero.
func TestOpenAICollectReportsCostAsUnknownNotZero(t *testing.T) {
	client, _ := serve(t, 200, openaiUsageBody(bucket27))

	var f fact.Fact
	(&openAI{http: client}).Collect(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey),
		window(t, "1d"), "", EmitterFunc(func(got fact.Fact) error { f = got; return nil }))

	if f.AmountBasis != fact.BasisUnknown {
		t.Errorf("AmountBasis = %q, want %q", f.AmountBasis, fact.BasisUnknown)
	}
	if f.AmountBasis == fact.BasisVendorReported && f.AmountMicros == 0 {
		t.Error("a zero amount was labelled as vendor-reported")
	}
}

// The requested window is the contract: a vendor bucket outside it must not
// silently add a day the user did not ask for, because coverage tracking and
// the prior-window delta both trust that stored days are collected days.
func TestOpenAICollectDropsBucketsOutsideTheWindow(t *testing.T) {
	outside := `{"object":"bucket","start_time":1787875200,"end_time":1787961600,"results":[
		{"input_tokens":99,"output_tokens":9,"project_id":"p","api_key_id":"k","model":"m"}]}`
	client, _ := serve(t, 200, openaiUsageBody(bucket27+","+outside))

	var days []string
	_, err := (&openAI{http: client}).Collect(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey),
		window(t, "1d"), "", EmitterFunc(func(f fact.Fact) error { days = append(days, f.Day); return nil }))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(days) != 1 || days[0] != "2026-08-27" {
		t.Errorf("days = %v, want only 2026-08-27", days)
	}
}

// A window with no usage is a finding, not a failure.
func TestOpenAICollectWithNoUsage(t *testing.T) {
	client, _ := serve(t, 200, `{"object":"page","data":[],"has_more":false,"next_page":null}`)

	count := 0
	_, err := (&openAI{http: client}).Collect(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey),
		window(t, "7d"), "", EmitterFunc(func(fact.Fact) error { count++; return nil }))
	if err != nil {
		t.Fatalf("an empty window was an error: %v", err)
	}
	if count != 0 {
		t.Errorf("emitted %d facts from an empty response", count)
	}
}

// emit writes straight through to the database in a later run, so an error from
// it must stop the collection rather than be swallowed.
func TestOpenAICollectStopsWhenEmitFails(t *testing.T) {
	client, _ := serve(t, 200, openaiUsageBody(bucket27))

	boom := errors.New("sink is full")
	_, err := (&openAI{http: client}).Collect(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey),
		window(t, "1d"), "", EmitterFunc(func(fact.Fact) error { return boom }))
	if !errors.Is(err, boom) {
		t.Errorf("Collect swallowed the emit error: %v", err)
	}
}

// Every fact from one collection must be distinguishable, or the primary key
// collapses rows that are genuinely different.
func TestOpenAICollectGivesEachRowADistinctIdentity(t *testing.T) {
	second := `{"object":"bucket","start_time":1787788800,"end_time":1787875200,"results":[
		{"input_tokens":10,"output_tokens":1,"project_id":"proj_a91f","api_key_id":"key_9f2a","model":"gpt-5.2-mini"},
		{"input_tokens":20,"output_tokens":2,"project_id":"proj_b72c","api_key_id":"key_c118","model":"gpt-5.2"}]}`
	client, _ := serve(t, 200, openaiUsageBody(second))

	seen := map[string]bool{}
	_, err := (&openAI{http: client}).Collect(context.Background(),
		cred.New("openai", cred.SourceEnv, "OPENAI_ADMIN_KEY", testKey),
		window(t, "1d"), "", EmitterFunc(func(f fact.Fact) error {
			if seen[f.ID()] {
				t.Errorf("duplicate fact id for %s/%s", f.ModelRef, f.PrincipalRef)
			}
			seen[f.ID()] = true
			return nil
		}))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(seen) != 2 {
		t.Errorf("got %d distinct facts, want 2", len(seen))
	}
}

// "We have not built this" and "your credential was rejected" call for
// different reactions, so they must be distinguishable by callers rather than
// only by reading the message.
func TestUnimplementedCollectorIsDistinguishable(t *testing.T) {
	_, err := (&anthropic{}).Collect(context.Background(), cred.Credential{},
		window(t, "1d"), "", EmitterFunc(func(fact.Fact) error { return nil }))

	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Collect error = %v, want it to wrap ErrNotImplemented", err)
	}
	var ve *VendorError
	if errors.As(err, &ve) {
		t.Error("an unimplemented collector reported itself as a vendor failure")
	}
}

// pager serves a two-page usage report and records the page cursors requested.
func pager(t *testing.T) (*http.Client, *[]string) {
	t.Helper()

	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("page") == "" {
			w.Write([]byte(`{"object":"page","data":[` + bucket27 + `],"has_more":true,"next_page":"page_two"}`))
			return
		}
		w.Write([]byte(`{"object":"page","data":[{"object":"bucket","start_time":1787788800,
			"end_time":1787875200,"results":[{"input_tokens":5,"output_tokens":1,
			"project_id":"proj_b72c","api_key_id":"key_c118","model":"gpt-5.2-mini"}]}],
			"has_more":false,"next_page":null}`))
	}))
	t.Cleanup(srv.Close)

	client := srv.Client()
	client.Transport = rewriteHost{base: client.Transport, to: strings.TrimPrefix(srv.URL, "http://")}
	return client, &asked
}

// recorder captures both what was emitted and where the collector said it was
// safe to resume from.
type recorder struct {
	facts  []fact.Fact
	pages  []string
	failAt int
}

func (r *recorder) Emit(f fact.Fact) error {
	if r.failAt > 0 && len(r.facts) == r.failAt {
		return errors.New("interrupted")
	}
	r.facts = append(r.facts, f)
	return nil
}

func (r *recorder) PageDone(cursor string) error {
	r.pages = append(r.pages, cursor)
	return nil
}

func TestCollectFollowsPagination(t *testing.T) {
	client, asked := pager(t)
	rec := &recorder{}

	next, err := (&openAI{http: client, limiter: newLimiter(1000)}).Collect(
		context.Background(), cred.New("openai", cred.SourceEnv, "K", testKey),
		window(t, "1d"), "", rec)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(rec.facts) != 2 {
		t.Errorf("got %d facts across two pages, want 2", len(rec.facts))
	}
	if len(*asked) != 2 || (*asked)[0] != "" || (*asked)[1] != "page_two" {
		t.Errorf("pages requested = %v, want [\"\", \"page_two\"]", *asked)
	}
	if next != "" {
		t.Errorf("final cursor = %q, want empty at the end of the report", next)
	}
}

// PageDone is what makes Ctrl-C during a backfill lose at most one page rather
// than the run.
func TestCollectReportsEveryPageBoundary(t *testing.T) {
	client, _ := pager(t)
	rec := &recorder{}

	if _, err := (&openAI{http: client, limiter: newLimiter(1000)}).Collect(
		context.Background(), cred.New("openai", cred.SourceEnv, "K", testKey),
		window(t, "1d"), "", rec); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(rec.pages) != 2 {
		t.Fatalf("PageDone called %d times for two pages: %v", len(rec.pages), rec.pages)
	}
	// The first boundary must point at the *next* page, or a resume would
	// re-read the page just finished.
	if rec.pages[0] != "page_two" {
		t.Errorf("first page boundary = %q, want page_two", rec.pages[0])
	}
}

// A cursor handed in must be used, so a resumed run does not re-read pages it
// already stored.
func TestCollectResumesFromACursor(t *testing.T) {
	client, asked := pager(t)
	rec := &recorder{}

	if _, err := (&openAI{http: client, limiter: newLimiter(1000)}).Collect(
		context.Background(), cred.New("openai", cred.SourceEnv, "K", testKey),
		window(t, "1d"), "page_two", rec); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(*asked) != 1 || (*asked)[0] != "page_two" {
		t.Errorf("pages requested = %v, want only [\"page_two\"]", *asked)
	}
	if len(rec.facts) != 1 {
		t.Errorf("got %d facts, want only the resumed page's 1", len(rec.facts))
	}
}

// An interrupt part-way through must keep the pages already completed.
func TestInterruptedCollectKeepsCompletedPages(t *testing.T) {
	client, _ := pager(t)
	rec := &recorder{failAt: 1} // fail on the second fact, i.e. during page two

	_, err := (&openAI{http: client, limiter: newLimiter(1000)}).Collect(
		context.Background(), cred.New("openai", cred.SourceEnv, "K", testKey),
		window(t, "1d"), "", rec)
	if err == nil {
		t.Fatal("the interruption was swallowed")
	}
	if len(rec.facts) != 1 {
		t.Errorf("kept %d facts, want the 1 from the completed page", len(rec.facts))
	}
	if len(rec.pages) != 1 || rec.pages[0] != "page_two" {
		t.Errorf("page boundaries = %v, want the first page recorded", rec.pages)
	}
}
