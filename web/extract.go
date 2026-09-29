package web

import (
	"bytes"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// extracted is a page reduced to what a person reading it would see.
type extracted struct {
	Title string
	Text  string
}

// skipTags never contribute text a reader sees.
var skipTags = map[string]bool{
	"script": true, "style": true, "template": true, "noscript": true,
	"iframe": true, "object": true, "embed": true, "svg": true, "canvas": true,
	"textarea": true, "select": true,
}

// blockTags start and end a line.
var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"section": true, "article": true, "header": true, "footer": true, "nav": true,
	"aside": true, "main": true, "blockquote": true, "pre": true, "table": true,
	"ul": true, "ol": true, "dl": true, "dt": true, "dd": true, "form": true,
	"fieldset": true, "figure": true, "figcaption": true, "address": true,
	"summary": true, "details": true,
}

// voidTags have no end tag, so they never open a skipped subtree.
var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "param": true,
	"source": true, "track": true, "wbr": true,
}

// hiddenStyle is the inline CSS a reader never sees: not rendered, not
// visible, zero-sized, transparent, or positioned off the page. Text hidden
// this way is the classic carrier for instructions aimed at a model rather
// than a person. A zero is a zero, not the start of 0.5: opacity:0.5 and
// font-size:0.9em are small print, which a reader sees; opacity:.0 is a
// zero written without its leading digit. Every property name is anchored
// to the start of the style or to the separator before it, so that
// margin-left:-100px is a margin and background-color:transparent is a
// background, neither of which hides anything.
var hiddenStyle = regexp.MustCompile(`(?i)(^|[;\s])(display\s*:\s*none|visibility\s*:\s*hidden|opacity\s*:\s*(0(\.0+)?|\.0+)([^.\d]|$)|font-size\s*:\s*0(\.0+)?(px|pt|em|rem|%)?([^.\d]|$)|color\s*:\s*transparent|(left|top|right|bottom|text-indent)\s*:\s*-\d{3,})`)

func hidden(attrs []html.Attribute) bool {
	for _, a := range attrs {
		switch strings.ToLower(a.Key) {
		case "hidden":
			return true
		case "aria-hidden":
			if strings.EqualFold(strings.TrimSpace(a.Val), "true") {
				return true
			}
		case "style":
			if hiddenStyle.MatchString(a.Val) {
				return true
			}
		}
	}
	return false
}

// extractText reduces a body to text by content type. Plain text passes
// through; the sanitiser does the rest. contentType may carry parameters
// ("text/plain; charset=utf-8"); only the media type decides.
func extractText(body []byte, contentType string) extracted {
	mediaType, _, _ := strings.Cut(contentType, ";")
	if strings.EqualFold(strings.TrimSpace(mediaType), "text/plain") {
		return extracted{Text: string(body)}
	}
	return extractHTML(body)
}

// extractHTML walks the tokens once. Scripts, styles, templates, frames,
// objects, comments and hidden subtrees are dropped; block elements become
// line breaks; the title is captured separately. Whitespace is left for the
// sanitiser to collapse.
//
// A self-closing tag that is not a void element (<div hidden/>) opens an
// element in a browser, so it opens a subtree here too; otherwise the text
// after it would be readable to the model and invisible to the person.
func extractHTML(body []byte) extracted {
	z := html.NewTokenizer(bytes.NewReader(body))
	var text, title strings.Builder
	inTitle := false
	skipTag, skipDepth := "", 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		tok := z.Token()
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			if skipDepth > 0 {
				if tok.Data == skipTag && !voidTags[tok.Data] {
					skipDepth++
				}
				continue
			}
			if skipTags[tok.Data] || hidden(tok.Attr) {
				if !voidTags[tok.Data] {
					skipTag, skipDepth = tok.Data, 1
				}
				continue
			}
			if tok.Data == "title" {
				inTitle = true
			}
			if blockTags[tok.Data] {
				text.WriteByte('\n')
			}
			if tok.Data == "td" || tok.Data == "th" {
				text.WriteByte(' ')
			}
		case html.EndTagToken:
			if skipDepth > 0 {
				if tok.Data == skipTag {
					skipDepth--
				}
				continue
			}
			if tok.Data == "title" {
				inTitle = false
			}
			if blockTags[tok.Data] {
				text.WriteByte('\n')
			}
		case html.TextToken:
			if skipDepth > 0 {
				continue
			}
			if inTitle {
				title.WriteString(tok.Data)
				continue
			}
			text.WriteString(tok.Data)
		}
		// Comments and doctypes fall through and are dropped.
	}
	return extracted{Title: strings.TrimSpace(title.String()), Text: text.String()}
}
