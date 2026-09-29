package web

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/garm-ai/tool-go/toolbind"
)

// Fetcher performs one governed fetch. Build one with NewFetcher and hand
// it to NewService.
type Fetcher struct {
	policy   *Policy
	client   *http.Client
	now      func() time.Time
	log      *slog.Logger
	resolver Resolver
	dial     DialFunc
	tls      *tls.Config
}

// Option configures a Fetcher at construction. The defaults are what
// production wants; every option exists so a test can stand up a local
// server behind a fake resolver and a real policy.
type Option func(*Fetcher)

// WithResolver replaces DNS. Tests map names to addresses; production
// leaves the default, net.DefaultResolver.
func WithResolver(r Resolver) Option { return func(f *Fetcher) { f.resolver = r } }

// WithDial replaces the raw TCP dial that runs AFTER the address check.
func WithDial(d DialFunc) Option { return func(f *Fetcher) { f.dial = d } }

// WithTLSConfig sets the client TLS configuration; tests trust a local
// certificate with it.
func WithTLSConfig(c *tls.Config) Option { return func(f *Fetcher) { f.tls = c } }

// WithClock replaces time.Now, so fetched_at is deterministic in tests.
func WithClock(now func() time.Time) Option { return func(f *Fetcher) { f.now = now } }

// WithLogger sets where the one log line per call goes.
func WithLogger(l *slog.Logger) Option { return func(f *Fetcher) { f.log = l } }

// NewFetcher builds the HTTP client around the policy: no proxy, ever (a
// proxy would carry the request past the floor), a guarded dialer, and a
// redirect check that re-runs the whole URL check on every hop.
func NewFetcher(p *Policy, opts ...Option) *Fetcher {
	f := &Fetcher{
		policy:   p,
		now:      time.Now,
		log:      slog.Default(),
		resolver: net.DefaultResolver,
		dial:     (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	}
	for _, o := range opts {
		o(f)
	}
	tr := &http.Transport{
		Proxy:                  nil,
		DialContext:            guardedDial(f.resolver, f.dial),
		TLSClientConfig:        f.tls,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  p.Timeout,
		MaxResponseHeaderBytes: 64 << 10,
		IdleConnTimeout:        30 * time.Second,
		ForceAttemptHTTP2:      false,
	}
	// No cookie jar: a page must not be able to set state that a later
	// fetch, by anyone, carries back.
	f.client = &http.Client{Transport: tr, CheckRedirect: f.checkRedirect}
	return f
}

// page is what fetch returns: bytes the site sent, capped, and where they
// came from.
type page struct {
	FinalURL    *url.URL
	Status      int
	ContentType string
	Body        []byte
	Truncated   bool
}

// checkURL is everything that can be decided before DNS: scheme,
// credentials, the host floor, the block list, the allow list. Run on the
// request URL and again on every redirect target. Every message names the
// host at most, never the path or the query.
func (f *Fetcher) checkURL(u *url.URL) error {
	if u.Scheme != "https" {
		return refusal("403", fmt.Sprintf("refused: scheme %q is not https", u.Scheme))
	}
	if u.User != nil {
		return refusal("403", "refused: the URL carries credentials")
	}
	host := hostOf(u)
	if err := checkHostFloor(host); err != nil {
		return err
	}
	return f.policy.CheckHost(host)
}

// checkRedirect is the http.Client hook. Capped, and every hop is checked
// as if it were the original request. A guard in an agent's manifest sees
// the URL the model wrote and never a Location header, so this is the only
// place a page on an allowed host that 302s to a blocked one is stopped.
func (f *Fetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	// The client sets Referer from the previous hop before calling this
	// hook. It would carry the caller's URL, query included, to a host the
	// page chose; nothing of the caller's request leaves for a hop it did
	// not name.
	req.Header.Del("Referer")
	if len(via) > f.policy.MaxRedirects {
		return refusal("403", fmt.Sprintf("refused: more than %d redirects", f.policy.MaxRedirects))
	}
	return f.checkHop(req.URL)
}

// maxHostRunes is the longest name DNS can carry. A redirect target with a
// longer host is not a host, and its text is not echoed.
const maxHostRunes = 253

// maxOriginRunes caps what a refusal repeats of a redirect target.
const maxOriginRunes = 256

// checkHop is checkURL for a redirect target: the same checks, but the
// target is the page's choice, so every refusal is 403 and the message
// repeats at most the target's origin, capped, and only when its scheme is
// one a reader would recognise.
func (f *Fetcher) checkHop(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return refusal("403", "redirect refused: the target is not an https URL")
	}
	if u.Hostname() == "" {
		return refusal("403", "redirect refused: the target has no host")
	}
	if utf8.RuneCountInString(u.Host) > maxHostRunes {
		return refusal("403", "redirect refused: the target host is not a valid name")
	}
	err := f.checkURL(u)
	if err == nil {
		return nil
	}
	// The target's origin and nothing more: what url_origin would let a
	// caller see of final_url.
	msg := "refused"
	var coded toolbind.CodedError
	if errors.As(err, &coded) {
		msg = coded.Message
	}
	return refusal("403", "redirect to "+capRunes(origin(u), maxOriginRunes)+" "+msg)
}

// knownTypes are media types a refusal may repeat verbatim: well known,
// and so not a channel for a page to write into the model's error text.
var knownTypes = map[string]bool{
	"application/pdf": true, "application/json": true, "application/xml": true,
	"application/octet-stream": true, "application/zip": true, "application/gzip": true,
	"application/javascript": true, "application/rss+xml": true, "application/atom+xml": true,
	"application/msword": true, "application/vnd.ms-excel": true,
	"text/css": true, "text/javascript": true, "text/csv": true, "text/xml": true,
	"text/markdown": true, "text/calendar": true,
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"image/svg+xml": true, "image/avif": true, "image/x-icon": true,
	"audio/mpeg": true, "audio/ogg": true, "video/mp4": true, "video/webm": true,
	"font/woff": true, "font/woff2": true, "font/ttf": true,
}

// knownTopLevel are the registered top-level media types. Anything else in
// a Content-Type header is reported as "unknown".
var knownTopLevel = map[string]bool{
	"application": true, "audio": true, "font": true, "image": true, "message": true,
	"model": true, "multipart": true, "text": true, "video": true,
}

// describeType is what a 415 says about a content type: the type itself
// if it is well known, its top level with a wildcard if only that is, and
// "unknown" otherwise. The upstream's own words never reach the message.
func describeType(mt string) string {
	if knownTypes[mt] {
		return mt
	}
	if top, _, ok := strings.Cut(mt, "/"); ok && knownTopLevel[top] {
		return top + "/*"
	}
	return "unknown"
}

// fetch gets u under the policy's caps. Every failure is a CodedError whose
// message carries nothing the page wrote.
func (f *Fetcher) fetch(ctx context.Context, u *url.URL) (*page, error) {
	invocation := ctx
	ctx, cancel := context.WithTimeout(ctx, f.policy.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, refusal("400", "refused: the URL could not be requested")
	}
	req.Header.Set("User-Agent", f.policy.UserAgent)
	req.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain;q=0.9")

	resp, err := f.client.Do(req)
	if err != nil {
		var coded toolbind.CodedError
		if errors.As(err, &coded) {
			return nil, coded
		}
		if timedOut(err, ctx) {
			return nil, f.timeout(invocation)
		}
		// A Location the client could not parse fails before the redirect
		// hook runs, as a plain error whose text quotes the header. It is
		// a redirect refusal like any other, and the header is not
		// repeated.
		if strings.Contains(err.Error(), "failed to parse Location header") {
			return nil, refusal("403", "redirect refused: the target could not be parsed")
		}
		return nil, refusal("502", "upstream: the connection failed")
	}
	defer resp.Body.Close()

	// Redirects were followed or refused above; a 3xx still here carried
	// no Location and is not a page.
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, refusal("404", "the page does not exist (upstream 404)")
	case resp.StatusCode >= 300:
		return nil, refusal("502", fmt.Sprintf("upstream answered %d", resp.StatusCode))
	}

	// Gated here, before a byte of body is read or handed to extraction:
	// only what extractText knows how to reduce reaches it.
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch mt {
	case "text/html", "application/xhtml+xml", "text/plain":
	default:
		return nil, refusal("415", fmt.Sprintf("unsupported content type %q; only html and plain text are fetched", describeType(mt)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.policy.MaxBodyBytes+1))
	if err != nil {
		if timedOut(err, ctx) {
			return nil, f.timeout(invocation)
		}
		return nil, refusal("502", "upstream: the body could not be read")
	}
	truncated := int64(len(body)) > f.policy.MaxBodyBytes
	if truncated {
		body = body[:f.policy.MaxBodyBytes]
	}
	return &page{
		FinalURL:    resp.Request.URL,
		Status:      resp.StatusCode,
		ContentType: mt,
		Body:        body,
		Truncated:   truncated,
	}, nil
}

// timedOut says whether err is the clock and not the site: the context's
// deadline (the policy's or the invocation's), or the transport's own
// header timeout, which fires at the same moment as the policy's and may
// be reported first.
func timedOut(err error, ctx context.Context) bool {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var to interface{ Timeout() bool }
	return errors.As(err, &to) && to.Timeout()
}

// timeout names which clock ran out. The invocation's deadline is garmd's
// and is shorter than the policy's when it fires first; saying "after 30s"
// about a fetch that lasted 100ms would mislead the caller. A cancelled
// invocation is not a deadline at all: the caller went away.
func (f *Fetcher) timeout(invocation context.Context) error {
	switch {
	case errors.Is(invocation.Err(), context.Canceled):
		return refusal("504", "cancelled: the invocation was cancelled before the page arrived")
	case invocation.Err() != nil:
		return refusal("504", "timed out: the invocation deadline passed")
	}
	return refusal("504", fmt.Sprintf("timed out after %s", f.policy.Timeout))
}

// hostOf normalises a URL's host the way every check here compares it.
func hostOf(u *url.URL) string {
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

// origin is scheme://host[:port]: what a caller who may not read a URL's
// path is still shown of it.
func origin(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

// capRunes cuts s to n runes.
func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
