package search_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/garm-ai/contracts/callctx"
	toolv1 "github.com/garm-ai/contracts/garm/tool/v1"
	"github.com/garm-ai/tool-go/toolbind"
	"github.com/garm-ai/tools/search"
	searchv1 "github.com/garm-ai/tools/search/gen/search/v1"
)

// index is a local https server standing in for the search vendor.
//
// The searcher never sees its address. A fake resolver maps
// api.search.brave.com to the public address it would have, the floor
// checks that, and only then does a fake dial connect: to the local
// listener, whatever address it was handed, recording what it was handed.
// So every test runs the real egress floor against the real endpoint name
// and the real TLS handshake, and a private-address refusal is proven by
// the dial never having been asked.
type index struct {
	srv   *httptest.Server
	addrs map[string][]netip.Addr

	mu       sync.Mutex
	lookups  []string
	dialed   []string
	requests []*http.Request
	// answer is what the endpoint replies with; a test replaces it.
	answer func(w http.ResponseWriter, r *http.Request)
}

var publicAddr = netip.MustParseAddr("93.184.216.34")

// The endpoint name every test's policy uses, and a private-address twin
// so the egress floor can be exercised against a name that resolves the
// wrong way.
const (
	endpointHost    = "search.example.com"
	endpointURL     = "https://" + endpointHost + "/res/v1/web/search"
	privateHost     = "private-index.example.com"
	privateEndpoint = "https://" + privateHost + "/search"
)

func newIndex(t *testing.T) *index {
	t.Helper()
	ix := &index{
		addrs: map[string][]netip.Addr{
			endpointHost: {publicAddr},
			privateHost:  {netip.MustParseAddr("10.1.2.3")},
		},
	}
	ix.answer = func(w http.ResponseWriter, _ *http.Request) { writeResults(w, defaultResults...) }
	ix.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ix.mu.Lock()
		ix.requests = append(ix.requests, r.Clone(context.Background()))
		answer := ix.answer
		ix.mu.Unlock()
		answer(w, r)
	}))
	t.Cleanup(ix.srv.Close)
	return ix
}

func (ix *index) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	ix.mu.Lock()
	ix.lookups = append(ix.lookups, host)
	ix.mu.Unlock()
	addrs, ok := ix.addrs[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	return addrs, nil
}

func (ix *index) dial(_ context.Context, _ string, address string) (net.Conn, error) {
	ix.mu.Lock()
	ix.dialed = append(ix.dialed, address)
	ix.mu.Unlock()
	return net.Dial("tcp", ix.srv.Listener.Addr().String())
}

func (ix *index) dials() []string {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return append([]string(nil), ix.dialed...)
}

func (ix *index) lastRequest(t *testing.T) *http.Request {
	t.Helper()
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if len(ix.requests) == 0 {
		t.Fatal("the endpoint was never asked")
	}
	return ix.requests[len(ix.requests)-1]
}

func (ix *index) replies(f func(w http.ResponseWriter, r *http.Request)) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.answer = f
}

// braveRow is one row of the vendor's JSON, written out here rather than
// imported: this is the wire format the client parses, and a test that
// shared a struct with the parser would assert nothing about it.
type braveRow struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

var defaultResults = []braveRow{
	{URL: "https://www.gov.uk/guidance/one", Title: "One", Description: "The first answer."},
	{URL: "https://bankofengland.co.uk/two", Title: "Two", Description: "The second answer."},
}

func writeResults(w http.ResponseWriter, rows ...braveRow) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"web": map[string]any{"results": rows},
	})
}

// fixedNow is what searched_at reports in every test.
var fixedNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

const testAPIKey = "test-subscription-token"

func (ix *index) searcher(t *testing.T, p *search.Policy, log *slog.Logger) *search.Searcher {
	t.Helper()
	tlsConf := ix.srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	return search.NewSearcher(p, testAPIKey,
		search.WithResolver(ix),
		search.WithDial(ix.dial),
		search.WithTLSConfig(tlsConf),
		search.WithClock(func() time.Time { return fixedNow }),
		search.WithLogger(log),
	)
}

func (ix *index) service(t *testing.T, yaml string, log *slog.Logger) *search.Service {
	t.Helper()
	return search.NewService(ix.searcher(t, testPolicy(t, yaml), log))
}

const testPolicyYAML = `
backend: brave
endpoint: "` + endpointURL + `"
allow:
  - "*.gov.uk"
  - bankofengland.co.uk
block:
  - blocked.gov.uk
max_snippet_chars: 200
timeout: 500ms
`

func testPolicy(t *testing.T, yaml string) *search.Policy {
	t.Helper()
	p, err := search.ParsePolicy(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var callSeq atomic.Int64

// invocation is the context garmd would have attached: a tenant, a subject,
// a call id. Never a token or a clearance.
func invocation() context.Context {
	n := callSeq.Add(1)
	return callctx.NewContext(context.Background(), &toolv1.InvocationContext{
		Attribution: &toolv1.CallContext{Tenant: "acme", CorrelationId: fmt.Sprintf("corr-%d", n)},
		Principal:   &toolv1.InvocationPrincipal{Subject: "user:ada", Kind: toolv1.PrincipalKind_PRINCIPAL_KIND_USER},
		CallId:      fmt.Sprintf("call-%d", n),
	})
}

func call(ctx context.Context, svc *search.Service, query string, limit uint32) (*searchv1.SearchWebResponse, error) {
	req := &searchv1.SearchWebRequest{Query: query}
	if limit != 0 {
		req.Limit = &limit
	}
	return svc.SearchWeb(ctx, req)
}

func code(t *testing.T, err error) (string, string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	var coded toolbind.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("not a CodedError: %v", err)
	}
	return coded.Code, coded.Message
}

func expect(t *testing.T, err error, wantCode, wantIn string) {
	t.Helper()
	c, m := code(t, err)
	if c != wantCode || !strings.Contains(m, wantIn) {
		t.Errorf("got %s %q, want %s containing %q", c, m, wantCode, wantIn)
	}
}
