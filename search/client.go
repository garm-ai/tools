package search

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/garm-ai/tool-go/toolbind"
)

// braveEndpoint is the Brave Search API's web search method. The default
// when a policy names the backend and leaves the endpoint unset.
//
// Brave rather than an LLM-backed provider (Tavily, Perplexity, Exa) for
// one reason that matters behind a governance plane: Brave returns rows out
// of an index, so a title and an excerpt are text the page's own author
// wrote. With an LLM-backed provider the model on the other side chooses
// the URLs and writes the titles and descriptions, which is a strictly
// larger injection surface pointed at this tenant's model, and Hermes's own
// documentation warns about exactly that. Brave also takes the credential
// in a header rather than a query parameter, so the key never reaches a URL
// that something in the path might log.
const braveEndpoint = "https://api.search.brave.com/res/v1/web/search"

// hit is one result as a backend hands it over, before anything is checked,
// cleaned or wrapped. Every string in it was written outside the tenant.
type hit struct {
	URL     string
	Title   string
	Snippet string
}

// Searcher performs one governed search. Build one with NewSearcher and
// hand it to NewService.
type Searcher struct {
	policy   *Policy
	apiKey   string
	client   *http.Client
	now      func() time.Time
	log      *slog.Logger
	resolver Resolver
	dial     DialFunc
	tls      *tls.Config
}

// Option configures a Searcher at construction. The defaults are what
// production wants; every option exists so a test can stand up a local
// endpoint behind a fake resolver and a real policy.
type Option func(*Searcher)

// WithResolver replaces DNS. Tests map names to addresses; production
// leaves the default, net.DefaultResolver.
func WithResolver(r Resolver) Option { return func(s *Searcher) { s.resolver = r } }

// WithDial replaces the raw TCP dial that runs AFTER the address check.
func WithDial(d DialFunc) Option { return func(s *Searcher) { s.dial = d } }

// WithTLSConfig sets the client TLS configuration; tests trust a local
// certificate with it.
func WithTLSConfig(c *tls.Config) Option { return func(s *Searcher) { s.tls = c } }

// WithClock replaces time.Now, so searched_at is deterministic in tests.
func WithClock(now func() time.Time) Option { return func(s *Searcher) { s.now = now } }

// WithLogger sets where the one log line per call goes.
func WithLogger(l *slog.Logger) Option { return func(s *Searcher) { s.log = l } }

// NewSearcher builds the HTTP client around the policy: no proxy, ever (a
// proxy would carry the credential past the floor), a guarded dialer, and
// no redirects at all.
//
// apiKey is held here and nowhere else. It is never put in a URL, never
// logged, never returned, and never named in an error: this package's
// refusals repeat the caller's own input or a host, and never one byte of
// what the endpoint said back.
func NewSearcher(p *Policy, apiKey string, opts ...Option) *Searcher {
	s := &Searcher{
		policy:   p,
		apiKey:   apiKey,
		now:      time.Now,
		log:      slog.Default(),
		resolver: net.DefaultResolver,
		dial:     (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	}
	for _, o := range opts {
		o(s)
	}
	tr := &http.Transport{
		Proxy:                  nil,
		DialContext:            guardedDial(s.resolver, s.dial),
		TLSClientConfig:        s.tls,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  p.Timeout,
		MaxResponseHeaderBytes: 64 << 10,
		IdleConnTimeout:        30 * time.Second,
		ForceAttemptHTTP2:      true,
	}
	// No cookie jar, and no redirects: this request carries a credential,
	// and a Location header is the endpoint asking for it to be sent
	// somewhere else. fetch_page follows up to five re-checked hops because
	// a page moves; a search API does not.
	s.client = &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return s
}

// search asks the endpoint for at most limit results and returns what it
// said, unchecked and uncleaned. Every failure is a CodedError whose
// message carries nothing the endpoint wrote.
func (s *Searcher) search(ctx context.Context, query string, limit int) ([]hit, error) {
	invocation := ctx
	ctx, cancel := context.WithTimeout(ctx, s.policy.Timeout)
	defer cancel()

	u := *s.policy.EndpointURL()
	q := url.Values{}
	q.Set("q", query)
	q.Set("count", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, refusal("500", "refused: the endpoint could not be requested")
	}
	req.Header.Set("User-Agent", s.policy.UserAgent)
	req.Header.Set("Accept", "application/json")
	// The credential. A header, not a query parameter: a URL is what ends
	// up in an access log, a referer and a redirect.
	req.Header.Set("X-Subscription-Token", s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		var coded toolbind.CodedError
		if errors.As(err, &coded) {
			return nil, coded
		}
		if timedOut(err, ctx) {
			return nil, s.timeout(invocation)
		}
		return nil, refusal("502", "upstream: the connection to the search endpoint failed")
	}
	defer resp.Body.Close()

	// 3xx is still here because redirects are not followed. The endpoint
	// asking for the credential to go elsewhere is a refusal, not a hop.
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, refusal("502", "upstream: the search endpoint redirected; a request carrying a credential is not redirected")
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		// Said plainly and without the endpoint's words, because this is
		// the one upstream failure an operator can fix.
		return nil, refusal("502", "upstream: the search endpoint rejected this deployment's credential")
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, refusal("429", "the search endpoint is rate limiting this deployment; try again later")
	case resp.StatusCode != http.StatusOK:
		return nil, refusal("502", fmt.Sprintf("upstream: the search endpoint answered %d", resp.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, s.policy.MaxBodyBytes+1))
	if err != nil {
		if timedOut(err, ctx) {
			return nil, s.timeout(invocation)
		}
		return nil, refusal("502", "upstream: the answer could not be read")
	}
	if int64(len(body)) > s.policy.MaxBodyBytes {
		// Not truncated and parsed: a JSON document cut in half is not a
		// document, and guessing at half of one is how a parser is taught
		// to accept whatever it is fed.
		return nil, refusal("502", fmt.Sprintf("upstream: the answer is larger than %d bytes", s.policy.MaxBodyBytes))
	}
	return decodeBrave(body)
}

// braveAnswer is the shape this client reads out of Brave's web search
// response. Only the three fields the contract has a home for; everything
// else the vendor sends is ignored rather than passed through, because a
// field nobody looked at is a field nobody checked.
type braveAnswer struct {
	Web struct {
		Results []struct {
			URL         string `json:"url"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

// decodeBrave turns Brave's answer into hits. A body that will not parse is
// an upstream failure and its text is never echoed: it is attacker-adjacent
// content, and an error message is context too.
func decodeBrave(body []byte) ([]hit, error) {
	var a braveAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, refusal("502", "upstream: the search endpoint's answer was not the JSON this backend expects")
	}
	out := make([]hit, 0, len(a.Web.Results))
	for _, r := range a.Web.Results {
		out = append(out, hit{URL: r.URL, Title: r.Title, Snippet: r.Description})
	}
	return out, nil
}

// timedOut says whether err is the clock and not the endpoint: the
// context's deadline (the policy's or the invocation's), or the transport's
// own header timeout, which fires at the same moment as the policy's and
// may be reported first.
func timedOut(err error, ctx context.Context) bool {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var to interface{ Timeout() bool }
	return errors.As(err, &to) && to.Timeout()
}

// timeout names which clock ran out. The invocation's deadline is garmd's
// and is shorter than the policy's when it fires first; saying "after 10s"
// about a search that lasted 100ms would mislead the caller. A cancelled
// invocation is not a deadline at all: the caller went away.
func (s *Searcher) timeout(invocation context.Context) error {
	switch {
	case errors.Is(invocation.Err(), context.Canceled):
		return refusal("504", "cancelled: the invocation was cancelled before the results arrived")
	case invocation.Err() != nil:
		return refusal("504", "timed out: the invocation deadline passed")
	}
	return refusal("504", fmt.Sprintf("timed out after %s", s.policy.Timeout))
}
