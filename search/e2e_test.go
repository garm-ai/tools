package search_test

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/garm-ai/contracts/callctx"
	"github.com/garm-ai/tool-go/garmtool"
	"github.com/garm-ai/tools/sanitize"
	"github.com/garm-ai/tools/search"
	searchv1 "github.com/garm-ai/tools/search/gen/search/v1"
)

// search_web end to end over a real broker: the generated binding, the
// tool-go runtime, the Garm-Invocation header, and the fake index from
// fixture_test.go. What it asserts is that the tool answers on the subject
// the contract derives, that a refusal travels as a coded error, and that a
// request without an invocation context never reaches the handler. Who may
// call and what they see of the answer is the daemon's, which is not in
// this picture.

const route = "/search.v1.SearchService/SearchWeb"

func runSearch(t *testing.T) (*nats.Conn, *index) {
	t.Helper()
	// Port -1 is an ephemeral port the kernel picks, so nothing here needs
	// the standard 4222 and two runs never collide.
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

	ix := newIndex(t)
	s := ix.searcher(t, testPolicy(t, testPolicyYAML), slog.New(slog.DiscardHandler))
	svc := garmtool.New("search", "v0.1.0", garmtool.WithConcurrency(4))
	if err := searchv1.ServeSearchService(svc, search.NewService(s)); err != nil {
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
				t.Errorf("search: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("search did not drain")
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := request(nc, route, nil, 100*time.Millisecond, true); err == nil {
			return nc, ix
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

func TestSearchWebAnswersOnItsContractSubject(t *testing.T) {
	nc, _ := runSearch(t)
	body, err := proto.Marshal(&searchv1.SearchWebRequest{Query: "capital requirements"})
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
	var resp searchv1.SearchWebResponse
	if err := proto.Unmarshal(msg.Data, &resp); err != nil {
		t.Fatal(err)
	}
	if n := len(resp.GetResults()); n != 2 {
		t.Fatalf("%d results, want 2", n)
	}
	first := resp.GetResults()[0]
	if first.GetUrl() != "https://www.gov.uk/guidance/one" {
		t.Errorf("url = %q", first.GetUrl())
	}
	if !strings.HasPrefix(first.GetSnippet(), sanitize.BeginMarker) {
		t.Errorf("snippet is not wrapped:\n%s", first.GetSnippet())
	}
	// The three fields a caller keys on, over the wire.
	if len(resp.GetResultsSha256()) != 64 {
		t.Errorf("results_sha256 = %q", resp.GetResultsSha256())
	}
	if resp.GetPolicyDigest() != testPolicy(t, testPolicyYAML).Digest() {
		t.Errorf("policy_digest = %q", resp.GetPolicyDigest())
	}
	if resp.GetSearchedAt() == "" {
		t.Error("searched_at is empty")
	}
}

// A coded refusal, not a bare error: garmd maps the code for the caller, and
// a plain error would arrive as an unclassified 500.
func TestARefusalTravelsAsACodedError(t *testing.T) {
	nc, ix := runSearch(t)
	ix.replies(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})
	body, _ := proto.Marshal(&searchv1.SearchWebRequest{Query: "q"})
	msg, err := request(nc, route, body, 3*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := msg.Header.Get("Nats-Service-Error-Code"); c != "429" {
		t.Errorf("code %q, want 429", c)
	}
	m := msg.Header.Get("Nats-Service-Error")
	if !strings.Contains(m, "rate limiting") {
		t.Errorf("message %q does not say what happened", m)
	}
	if strings.Contains(m, "slow down") {
		t.Errorf("the refusal repeated the endpoint's words: %q", m)
	}
}

func TestARequestWithoutAnInvocationContextNeverReachesTheHandler(t *testing.T) {
	nc, ix := runSearch(t)
	body, _ := proto.Marshal(&searchv1.SearchWebRequest{Query: "q"})
	msg, err := request(nc, route, body, 3*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if c := msg.Header.Get("Nats-Service-Error-Code"); c != "400" {
		t.Errorf("code %q, want 400", c)
	}
	if d := ix.dials(); len(d) != 0 {
		t.Errorf("a request with no invocation context spent a search: %v", d)
	}
}

func TestTheServiceAdvertisesTheContractItWasBuiltFrom(t *testing.T) {
	if searchv1.DescriptorHash == "" || searchv1.ContractVersion == "" {
		t.Fatal("no descriptor hash or contract version; the daemon cannot tell what this serves")
	}
}
