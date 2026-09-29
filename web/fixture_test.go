package web_test

import (
	"context"
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

	"github.com/garm-ai/garm/contracts/callctx"
	toolv1 "github.com/garm-ai/garm/contracts/garm/tool/v1"
	"github.com/garm-ai/tool-go/toolbind"
	"github.com/garm-ai/tools/web"
	webv1 "github.com/garm-ai/tools/web/gen/web/v1"
)

// site is a local https server standing in for the whole internet.
//
// The fetcher never sees its address. A fake resolver maps names to the
// public addresses they would have, the floor checks those, and only then
// does a fake dial connect: to the local listener, whatever address it was
// handed, recording what it was handed. So every test runs the real floor
// against real names and the real TLS handshake (httptest's certificate
// covers example.com and *.example.com), and a private-address refusal is
// proven by the dial never having been asked.
type site struct {
	srv   *httptest.Server
	addrs map[string][]netip.Addr

	mu      sync.Mutex
	lookups []string
	dialed  []string
}

var publicAddr = netip.MustParseAddr("93.184.216.34")

// downAddr is public and passes the floor, and the fake dial refuses it:
// a host that is allowed, resolves, and cannot be reached.
var downAddr = netip.MustParseAddr("93.184.216.35")

func newSite(t *testing.T, h http.Handler) *site {
	t.Helper()
	s := &site{
		srv: httptest.NewTLSServer(h),
		addrs: map[string][]netip.Addr{
			"example.com":          {publicAddr},
			"www.example.com":      {publicAddr},
			"blocked.example.com":  {publicAddr},
			"internal.example.com": {netip.MustParseAddr("10.1.2.3")},
			"mixed.example.com":    {publicAddr, netip.MustParseAddr("10.1.2.3")},
			"v6.example.com":       {netip.MustParseAddr("::ffff:10.1.2.3")},
			"down.example.com":     {downAddr},
		},
	}
	t.Cleanup(s.srv.Close)
	return s
}

func (s *site) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	s.mu.Lock()
	s.lookups = append(s.lookups, host)
	s.mu.Unlock()
	addrs, ok := s.addrs[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	return addrs, nil
}

func (s *site) dial(_ context.Context, _ string, address string) (net.Conn, error) {
	s.mu.Lock()
	s.dialed = append(s.dialed, address)
	s.mu.Unlock()
	if host, _, _ := net.SplitHostPort(address); host == downAddr.String() {
		// A plain error, as net.Dialer returns one: not a CodedError.
		return nil, errors.New("connect: connection refused")
	}
	return net.Dial("tcp", s.srv.Listener.Addr().String())
}

func (s *site) dials() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.dialed...)
}

func (s *site) resolved() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lookups...)
}

// fixedNow is what fetched_at reports in every test.
var fixedNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func (s *site) fetcher(t *testing.T, p *web.Policy, log *slog.Logger) *web.Fetcher {
	t.Helper()
	tlsConf := s.srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	return web.NewFetcher(p,
		web.WithResolver(s),
		web.WithDial(s.dial),
		web.WithTLSConfig(tlsConf),
		web.WithClock(func() time.Time { return fixedNow }),
		web.WithLogger(log),
	)
}

const testPolicyYAML = `
allow:
  - example.com
  - "*.example.com"
block:
  - blocked.example.com
max_body_bytes: 4096
max_chars: 3000
timeout: 500ms
`

func testPolicy(t *testing.T, yaml string) *web.Policy {
	t.Helper()
	p, err := web.ParsePolicy(strings.NewReader(yaml))
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

func call(ctx context.Context, svc *web.Service, rawurl string, limit uint32) (*webv1.FetchPageResponse, error) {
	req := &webv1.FetchPageRequest{Url: rawurl}
	if limit != 0 {
		req.CharLimit = &limit
	}
	return svc.FetchPage(ctx, req)
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
