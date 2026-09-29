package web_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/garm-ai/garm/contracts/callctx"
	"github.com/garm-ai/tool-go/garmtool"
	"github.com/garm-ai/tools/sanitize"
	"github.com/garm-ai/tools/web"
	webv1 "github.com/garm-ai/tools/web/gen/web/v1"
)

// fetch_page end to end over a real broker: the generated binding, the
// tool-go runtime, the Garm-Invocation header, and the fake internet from
// fixture_test.go. What it asserts is that the tool answers on the subject
// the contract derives, that a refusal travels as a coded error, and that a
// request without an invocation context never reaches the handler. Who may
// call and what they see of the answer is the daemon's, which is not in
// this picture.

const route = "/web.v1.WebService/FetchPage"

func runWeb(t *testing.T) (*nats.Conn, *site) {
	t.Helper()
	srv, err := natsserver.NewServer(&natsserver.Options{Port: -1, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats did not start")
	}
	t.Cleanup(srv.Shutdown)

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	s := newSite(t, fakeInternet())
	f := s.fetcher(t, testPolicy(t, testPolicyYAML), slog.New(slog.DiscardHandler))
	svc := garmtool.New("web", "v0.1.1", garmtool.WithConcurrency(4))
	if err := webv1.ServeWebService(svc, web.NewService(f)); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx, nc) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && ctx.Err() == nil {
				t.Errorf("web: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("web did not drain")
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := request(nc, route, nil, 100*time.Millisecond, true); err == nil {
			return nc, s
		}
	}
	t.Fatalf("%s never started answering", route)
	return nil, nil
}

// request sends body to the tool the way the daemon's transport does, with
// the one header a real garmd would already have set.
func request(nc *nats.Conn, route string, body []byte, timeout time.Duration, withHeader bool) (*nats.Msg, error) {
	msg := nats.NewMsg(garmtool.Subject(route))
	msg.Data = body
	if withHeader {
		h, err := callctx.Encode(callctx.FromContext(invocation()))
		if err != nil {
			return nil, err
		}
		msg.Header.Set(callctx.Header, h)
	}
	return nc.RequestMsg(msg, timeout)
}

func TestFetchPageAnswersOnItsContractSubject(t *testing.T) {
	nc, _ := runWeb(t)
	body, err := proto.Marshal(&webv1.FetchPageRequest{Url: "https://example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := request(nc, route, body, 3*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := msg.Header.Get("Nats-Service-Error-Code"); c != "" {
		t.Fatalf("%s %s", c, msg.Header.Get("Nats-Service-Error"))
	}
	var resp webv1.FetchPageResponse
	if err := proto.Unmarshal(msg.Data, &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.GetContent(), sanitize.BeginMarker) || !strings.Contains(resp.GetContent(), "visible-text") {
		t.Errorf("content:\n%s", resp.GetContent())
	}
	if resp.GetTitle() != "Example Domain" {
		t.Errorf("title = %q", resp.GetTitle())
	}
	// The three fields a caller keys on, over the wire: the origin only,
	// a digest of the content as returned, and the policy that allowed it.
	if resp.GetFinalUrl() != "https://example.com/" {
		t.Errorf("final_url = %q, want the origin", resp.GetFinalUrl())
	}
	if len(resp.GetContentSha256()) != 64 {
		t.Errorf("content_sha256 = %q", resp.GetContentSha256())
	}
	if resp.GetPolicyDigest() != testPolicy(t, testPolicyYAML).Digest() {
		t.Errorf("policy_digest = %q", resp.GetPolicyDigest())
	}
}

func TestARefusalTravelsAsACodedError(t *testing.T) {
	nc, _ := runWeb(t)
	body, _ := proto.Marshal(&webv1.FetchPageRequest{Url: "https://blocked.example.com/"})
	msg, err := request(nc, route, body, 3*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := msg.Header.Get("Nats-Service-Error-Code"); c != "403" {
		t.Errorf("code %q, want 403", c)
	}
	if m := msg.Header.Get("Nats-Service-Error"); !strings.Contains(m, `block rule "blocked.example.com"`) {
		t.Errorf("message %q does not name the rule", m)
	}
}

func TestARequestWithoutAnInvocationContextNeverReachesTheHandler(t *testing.T) {
	nc, s := runWeb(t)
	body, _ := proto.Marshal(&webv1.FetchPageRequest{Url: "https://example.com/"})
	msg, err := request(nc, route, body, 3*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if c := msg.Header.Get("Nats-Service-Error-Code"); c != "400" {
		t.Errorf("code %q, want 400", c)
	}
	if d := s.dials(); len(d) != 0 {
		t.Errorf("a request with no invocation context fetched something: %v", d)
	}
}

func TestTheServiceAdvertisesTheContractItWasBuiltFrom(t *testing.T) {
	if webv1.DescriptorHash == "" || webv1.ContractVersion == "" {
		t.Fatal("no descriptor hash or contract version; the daemon cannot tell what this serves")
	}
}
