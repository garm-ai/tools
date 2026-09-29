package search

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	"github.com/garm-ai/contracts/callctx"
	"github.com/garm-ai/tool-go/toolbind"
	"github.com/garm-ai/tools/sanitize"
	searchv1 "github.com/garm-ai/tools/search/gen/search/v1"
)

// Service implements the generated SearchServiceHandler. One method, one
// tool.
type Service struct {
	s *Searcher
}

var _ searchv1.SearchServiceHandler = (*Service)(nil)

// NewService serves search_web with s.
func NewService(s *Searcher) *Service { return &Service{s: s} }

// SearchWeb is the tool. The order of checks is the order of cost: the
// invocation context, the clamp, then the one request to the endpoint, then
// the floor and the host policy over every URL that came back, then the
// sanitiser over every title and snippet. The one log line names the
// tenant, subject and call id from Garm-Invocation and how many results
// survived; it never names the query, because the query is the caller's
// text and a log is a place text goes to be read by people who were not on
// the call.
func (svc *Service) SearchWeb(ctx context.Context, req *searchv1.SearchWebRequest) (*searchv1.SearchWebResponse, error) {
	start := svc.s.now()
	ic := callctx.FromContext(ctx)
	if ic == nil {
		// tool-go refuses an unattributed call before it reaches a
		// handler; this is the service being safe without it, rather than
		// spending the deployment's search quota for nobody and ledgering
		// an empty tenant.
		err := refusal("400", "refused: no invocation context")
		svc.refused(ctx, []any{"tool", "search_web"}, err, start)
		return nil, err
	}
	attrs := []any{
		"tool", "search_web",
		"tenant", ic.GetAttribution().GetTenant(),
		"subject", ic.GetPrincipal().GetSubject(),
		"call_id", ic.GetCallId(),
	}

	query := strings.TrimSpace(req.GetQuery())
	if query == "" {
		err := refusal("400", "refused: the query is empty")
		svc.refused(ctx, attrs, err, start)
		return nil, err
	}
	if n := utf8.RuneCountInString(query); n > maxQueryRunes {
		// The chain validates max_len before the call arrives; this stays
		// because the service must be safe without it, and because the
		// query is the one field that leaves the tenant.
		err := refusal("400", "refused: the query is longer than the limit")
		svc.refused(ctx, attrs, err, start)
		return nil, err
	}
	limit := clampLimit(req.GetLimit(), svc.s.policy)

	hits, err := svc.s.search(ctx, query, limit)
	if err != nil {
		svc.refused(ctx, attrs, err, start)
		return nil, err
	}

	resp := svc.assemble(hits, limit, start)
	svc.s.log.InfoContext(ctx, "search_web", append(attrs,
		"limit", limit,
		"returned", len(resp.GetResults()),
		"dropped", resp.GetDropped(),
		"duration_ms", svc.s.now().Sub(start).Milliseconds(),
	)...)
	return resp, nil
}

// maxQueryRunes is the cap in search.proto. Repeated here because the
// service must be safe without the chain in front of it.
const maxQueryRunes = 512

// assemble turns what the endpoint said into what the caller sees: the
// floor and the host policy over every URL, the sanitiser over every title
// and snippet, and a count of what was dropped.
//
// A result that fails any check is DROPPED, not refused. One bad link in an
// index's answer is not a reason to fail a search, and a refusal would hand
// whoever poisoned that row a way to deny the tool to everyone.
func (svc *Service) assemble(hits []hit, limit int, start time.Time) *searchv1.SearchWebResponse {
	resp := &searchv1.SearchWebResponse{
		PolicyDigest: proto.String(svc.s.policy.Digest()),
		SearchedAt:   proto.String(start.UTC().Format(time.RFC3339)),
	}
	truncated := len(hits) > limit
	var dropped uint32
	var notices []string

	for _, h := range hits {
		if len(resp.Results) >= limit {
			truncated = true
			break
		}
		r, cut, ns, ok := svc.result(h)
		if !ok {
			dropped++
			continue
		}
		truncated = truncated || cut
		notices = mergeNotices(notices, ns)
		resp.Results = append(resp.Results, r)
	}

	resp.Dropped = proto.Uint32(dropped)
	resp.Truncated = proto.Bool(truncated)
	resp.Notices = notices
	resp.ResultsSha256 = proto.String(digestResults(resp.Results))
	return resp
}

// result checks and cleans one hit. ok is false when the URL did not pass
// the address floor or the host policy, or when nothing usable was left.
func (svc *Service) result(h hit) (r *searchv1.Result, truncated bool, notices []string, ok bool) {
	if utf8.RuneCountInString(h.URL) > MaxURLRunes {
		return nil, false, nil, false
	}
	u, err := url.Parse(h.URL)
	if err != nil || u.Host == "" {
		return nil, false, nil, false
	}
	if err := checkResultURL(u); err != nil {
		return nil, false, nil, false
	}
	if err := svc.s.policy.CheckHost(hostOf(u)); err != nil {
		return nil, false, nil, false
	}

	// Origin and path, never the query, the fragment or credentials, and
	// cleaned like any other text the index chose: a URL is a string an
	// index wrote, and a marker look-alike in one travels into the model
	// exactly as it would in a snippet. EscapedPath runs first, so an
	// invisible character inside a path is percent-encoded rather than
	// deleted — deleting one would hand the model an address the index
	// never returned, and the encoded form is visible, which was the point.
	link := sanitize.Clean(origin(u)+u.EscapedPath(), MaxURLRunes)
	link.Text = strings.TrimSuffix(link.Text, sanitize.TruncationNote)
	if link.Text == "" {
		return nil, false, nil, false
	}

	r = &searchv1.Result{Url: proto.String(link.Text)}
	truncated = link.Truncated
	notices = mergeNotices(notices, link.Notices)

	// The snippet gets the full treatment: cleaned, then wrapped with THIS
	// result's origin as its source. Per result, not per response, because
	// each result has a different author and a single wrapper around the
	// lot would tell the model one lie about where the text came from.
	if body := sanitize.Clean(h.Snippet, svc.s.policy.MaxSnippetChars); body.Text != "" {
		r.Snippet = proto.String(sanitize.Wrap(body, origin(u)))
		truncated = truncated || body.Truncated
		notices = mergeNotices(notices, body.Notices)
	}

	// The title is index text too: cleaned the same way, kept to one line,
	// and not wrapped, because it is a label beside a link rather than a
	// passage. Anything the sanitiser noticed in it joins the response's
	// notices all the same.
	if title := sanitize.Clean(h.Title, MaxTitleRunes); title.Text != "" {
		t := strings.TrimSuffix(title.Text, sanitize.TruncationNote)
		r.Title = proto.String(strings.ReplaceAll(t, "\n", " "))
		truncated = truncated || title.Truncated
		notices = mergeNotices(notices, title.Notices)
	}
	return r, truncated, notices, true
}

// digestResults is sha256, lowercase hex, over the results exactly as
// returned: the fields in order, each length-prefixed so no arrangement of
// one result's text can produce the digest of another arrangement.
func digestResults(rs []*searchv1.Result) string {
	h := sha256.New()
	var n [8]byte
	for _, r := range rs {
		for _, s := range []string{r.GetUrl(), r.GetTitle(), r.GetSnippet()} {
			binary.BigEndian.PutUint64(n[:], uint64(len(s)))
			h.Write(n[:])
			h.Write([]byte(s))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// refused logs a refusal by its code alone. A message can name a host the
// index chose; a code cannot.
func (svc *Service) refused(ctx context.Context, attrs []any, err error, start time.Time) {
	code := "500"
	var coded toolbind.CodedError
	if errors.As(err, &coded) {
		code = coded.Code
	}
	svc.s.log.WarnContext(ctx, "search_web refused", append(attrs,
		"code", code,
		"duration_ms", svc.s.now().Sub(start).Milliseconds(),
	)...)
}

// noticeOrder is the sanitiser's fixed order, kept when notice lists are
// merged across results.
var noticeOrder = []string{sanitize.NoticeInvisibleCharacters, sanitize.NoticeSentinels, sanitize.NoticeInjectionPhrase}

// mergeNotices unions two notice lists in the sanitiser's order. Nil when
// both are empty, so a clean answer reports no notices rather than an empty
// list.
func mergeNotices(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	var out []string
	for _, n := range noticeOrder {
		if slices.Contains(a, n) || slices.Contains(b, n) {
			out = append(out, n)
		}
	}
	return out
}

// clampLimit applies the request's limit inside the policy's ceiling. The
// chain validates the field's range before the call arrives; the clamp
// stays because the service must be safe without it.
func clampLimit(v uint32, p *Policy) int {
	limit := DefaultLimit
	if v != 0 {
		limit = int(v)
	}
	if limit < 1 {
		limit = 1
	}
	if limit > p.MaxResults {
		limit = p.MaxResults
	}
	return limit
}
