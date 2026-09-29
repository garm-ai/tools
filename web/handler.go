package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/garm-ai/garm/contracts/callctx"
	"github.com/garm-ai/tool-go/toolbind"
	"github.com/garm-ai/tools/sanitize"
	webv1 "github.com/garm-ai/tools/web/gen/web/v1"
)

// Service implements the generated WebServiceHandler. One method, one
// tool.
type Service struct {
	f *Fetcher
}

var _ webv1.WebServiceHandler = (*Service)(nil)

// NewService serves fetch_page with f.
func NewService(f *Fetcher) *Service { return &Service{f: f} }

// maxTitleRunes caps the title, which is one line of the response, not a
// second content field.
const maxTitleRunes = 200

// FetchPage is the tool. The order of checks is the order of cost: parse,
// then everything decidable before DNS, then the fetch (which re-checks
// the resolved address and every redirect), then extraction, then the
// sanitiser. The one log line names the tenant, subject and call id from
// Garm-Invocation and the host the caller asked for, and nothing the page
// sent.
func (s *Service) FetchPage(ctx context.Context, req *webv1.FetchPageRequest) (*webv1.FetchPageResponse, error) {
	start := s.f.now()
	ic := callctx.FromContext(ctx)
	if ic == nil {
		// tool-go refuses an unattributed call before it reaches a
		// handler; this is the service being safe without it, rather than
		// fetching for nobody and ledgering an empty tenant.
		err := refusal("400", "refused: no invocation context")
		s.refused(ctx, []any{"tool", "fetch_page"}, err, start)
		return nil, err
	}
	attrs := []any{
		"tool", "fetch_page",
		"tenant", ic.GetAttribution().GetTenant(),
		"subject", ic.GetPrincipal().GetSubject(),
		"call_id", ic.GetCallId(),
	}

	u, err := url.Parse(req.GetUrl())
	if err != nil || u.Host == "" {
		err := refusal("400", "refused: the URL could not be parsed")
		s.refused(ctx, attrs, err, start)
		return nil, err
	}
	attrs = append(attrs, "host", hostOf(u))

	if err := s.f.checkURL(u); err != nil {
		s.refused(ctx, attrs, err, start)
		return nil, err
	}
	limit := clampChars(req.GetCharLimit(), s.f.policy)

	pg, err := s.f.fetch(ctx, u)
	if err != nil {
		s.refused(ctx, attrs, err, start)
		return nil, err
	}

	ex := extractText(pg.Body, pg.ContentType)
	body := sanitize.Clean(ex.Text, limit)
	wrapped := sanitize.Wrap(body, origin(pg.FinalURL))
	sum := sha256.Sum256([]byte(wrapped))
	final := finalURL(pg.FinalURL)

	resp := &webv1.FetchPageResponse{
		FinalUrl:      proto.String(final.Text),
		HttpStatus:    proto.Uint32(uint32(pg.Status)),
		Content:       proto.String(wrapped),
		Truncated:     proto.Bool(pg.Truncated || body.Truncated || final.Truncated),
		ContentSha256: proto.String(hex.EncodeToString(sum[:])),
		PolicyDigest:  proto.String(s.f.policy.Digest()),
		FetchedAt:     proto.String(start.UTC().Format(time.RFC3339)),
		Notices:       mergeNotices(body.Notices, final.Notices),
	}
	// The title is page text too: cleaned the same way, and anything the
	// sanitiser noticed in it joins the response's notices. The wrapper
	// header carries the content's notices only, because that is what the
	// header sits on.
	if title := sanitize.Clean(ex.Title, maxTitleRunes); title.Text != "" {
		t := strings.TrimSuffix(title.Text, sanitize.TruncationNote)
		resp.Title = proto.String(strings.ReplaceAll(t, "\n", " "))
		resp.Notices = mergeNotices(resp.Notices, title.Notices)
	}

	s.f.log.InfoContext(ctx, "fetch_page", append(attrs,
		"status", pg.Status,
		"bytes", len(pg.Body),
		"truncated", resp.GetTruncated(),
		"duration_ms", s.f.now().Sub(start).Milliseconds(),
	)...)
	return resp, nil
}

// maxFinalURLRunes is the request URL's own cap (max_len in web.proto). A
// longer final_url can only have come from a Location a page chose.
const maxFinalURLRunes = 2048

// finalURL is what the response says about where the fetch ended: scheme,
// host and path, never the query, the fragment or credentials. After a
// redirect the URL is the page's choice: the host passed every check, the
// path is percent-encoded by net/url, and the whole is cleaned like any
// other page text (invisible characters and marker look-alikes cannot
// survive it) and capped at the request URL's own limit. A caller who may
// not read the path is shown the origin by url_origin.
func finalURL(u *url.URL) sanitize.Cleaned {
	c := sanitize.Clean(u.Scheme+"://"+u.Host+u.EscapedPath(), maxFinalURLRunes)
	c.Text = strings.TrimSuffix(c.Text, sanitize.TruncationNote)
	// A cut final_url says so: c.Truncated is folded into the response's
	// truncated flag by the caller, so a reader cannot mistake a cut path
	// for the whole one.
	return c
}

// refused logs a refusal by its code alone. The message can name a
// redirect target, which the page chose; the code cannot.
func (s *Service) refused(ctx context.Context, attrs []any, err error, start time.Time) {
	code := "500"
	var coded toolbind.CodedError
	if errors.As(err, &coded) {
		code = coded.Code
	}
	s.f.log.WarnContext(ctx, "fetch_page refused", append(attrs,
		"code", code,
		"duration_ms", s.f.now().Sub(start).Milliseconds(),
	)...)
}

// noticeOrder is the sanitiser's fixed order, kept when two Cleaned results
// are merged.
var noticeOrder = []string{sanitize.NoticeInvisibleCharacters, sanitize.NoticeSentinels, sanitize.NoticeInjectionPhrase}

// mergeNotices unions two notice lists in the sanitiser's order. Nil when
// both are empty, so a clean page reports no notices rather than an empty
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

// clampChars applies the request's char_limit inside the policy's ceiling.
// The chain validates the field's range before the call arrives; the clamp
// stays because the service must be safe without it.
func clampChars(v uint32, p *Policy) int {
	limit := DefaultMaxChars
	if v != 0 {
		limit = int(v)
	}
	if limit < MinChars {
		limit = MinChars
	}
	if limit > p.MaxChars {
		limit = p.MaxChars
	}
	return limit
}
