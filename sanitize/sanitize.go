// Package sanitize prepares text that somebody outside the tenant wrote —
// a fetched web page, a search snippet, an extracted document — for a model
// to read.
//
// It is not a boundary. Hermes Agent's own security statement is right that
// nothing inside the agent process constitutes containment, and garm's chain
// sits outside the process for that reason. What this package does is
// remove the cheap carriers an injection rides on (invisible characters,
// bidirectional overrides, chat-template tokens, look-alikes of our own
// markers), normalise what is left so two fetches of one page compare equal,
// cap its length, and wrap it so the runner and the model can see where the
// untrusted text begins and ends. Heuristic findings are annotated, never
// used to refuse: blocking on heuristics produces false positives and no
// security.
package sanitize

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// DefaultMaxRunes is the cap Clean applies when asked for zero. The same
// number as Hermes's web_extract default, which has held up in use.
const DefaultMaxRunes = 15000

// TruncationNote is appended to text Clean cut short, so a reader can tell
// a page that ends mid-sentence from one that was cut.
const TruncationNote = "\n[truncated]"

// The wrapper. Three angle brackets on each side because Clean guarantees
// no run of three or more survives in the text, so nothing inside the
// wrapper can open or close it.
const (
	BeginMarker = "<<<untrusted-content"
	EndMarker   = "<<<end-untrusted-content>>>"
)

// Notices Clean can attach. Stable strings: they are shown to the model in
// the wrapper header and may be asserted on by callers.
const (
	NoticeInvisibleCharacters = "invisible-characters-removed"
	NoticeSentinels           = "sentinels-neutralised"
	NoticeInjectionPhrase     = "injection-phrase"
)

// Cleaned is the result of Clean.
type Cleaned struct {
	// Text is NFC-normalised, stripped of control and invisible characters,
	// whitespace-collapsed, sentinel-neutralised and capped.
	Text string
	// Truncated says Text was cut at the cap and carries TruncationNote.
	Truncated bool
	// Notices records what Clean did or saw, in a fixed order. Never a
	// reason to refuse: an annotation for the reader.
	Notices []string
}

// injectionPhrases are annotated, not removed. A short list on purpose:
// every entry is a false positive waiting to happen on a page ABOUT prompt
// injection, and the notice is advisory.
var injectionPhrases = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bignore\s+(all\s+|any\s+)?(previous|prior|above|earlier)\s+(instructions?|prompts?|messages?)`),
	regexp.MustCompile(`(?i)\bdisregard\s+(all\s+|any\s+)?(previous|prior|above|earlier|your)\s+(instructions?|prompts?|rules?)`),
	regexp.MustCompile(`(?i)\byou\s+are\s+now\s+(a|an|the|in)\b`),
	regexp.MustCompile(`(?i)\bnew\s+(system\s+)?instructions?\s*:`),
	regexp.MustCompile(`(?i)\bdo\s+not\s+(tell|inform|reveal\s+to)\s+the\s+user\b`),
}

var (
	// Three or more of either bracket is what our own markers are made of.
	openRun  = regexp.MustCompile(`<{3,}`)
	closeRun = regexp.MustCompile(`>{3,}`)
	// Chat-template tokens: <|im_start|>, <|endoftext|>, <|system|> and kin.
	tokenOpen  = strings.NewReplacer("<|", "< |")
	tokenClose = strings.NewReplacer("|>", "| >")

	spaces      = regexp.MustCompile(`[ \t]+`)
	lineEdges   = regexp.MustCompile(` *\n *`)
	manyNewline = regexp.MustCompile(`\n{3,}`)
)

// Clean normalises s and caps it at maxRunes runes of text (zero means
// DefaultMaxRunes). Text past the cap is dropped and TruncationNote
// appended.
func Clean(s string, maxRunes int) Cleaned {
	if maxRunes <= 0 {
		maxRunes = DefaultMaxRunes
	}
	var c Cleaned

	s = strings.ToValidUTF8(s, string(utf8.RuneError))
	s = norm.NFC.String(s)

	var b strings.Builder
	b.Grow(len(s))
	dropped := false
	for _, r := range s {
		switch classify(r) {
		case keep:
			b.WriteRune(r)
		case newline:
			b.WriteByte('\n')
		case space:
			b.WriteByte(' ')
		case drop:
			dropped = true
		}
	}
	s = b.String()
	if dropped {
		c.Notices = append(c.Notices, NoticeInvisibleCharacters)
	}

	s = spaces.ReplaceAllString(s, " ")
	s = lineEdges.ReplaceAllString(s, "\n")
	s = manyNewline.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)

	if t := neutralise(s); t != s {
		s = t
		c.Notices = append(c.Notices, NoticeSentinels)
	}

	for _, re := range injectionPhrases {
		if re.MatchString(s) {
			c.Notices = append(c.Notices, NoticeInjectionPhrase)
			break
		}
	}

	if utf8.RuneCountInString(s) > maxRunes {
		rs := []rune(s)
		s = strings.TrimSpace(string(rs[:maxRunes])) + TruncationNote
		c.Truncated = true
	}
	c.Text = s
	return c
}

// neutralise rewrites anything that could read as one of our markers or as
// a chat-template token. Runs of three or more brackets collapse to two, so
// BeginMarker and EndMarker cannot be reconstituted from cleaned text.
func neutralise(s string) string {
	s = openRun.ReplaceAllString(s, "<<")
	s = closeRun.ReplaceAllString(s, ">>")
	s = tokenOpen.Replace(s)
	s = tokenClose.Replace(s)
	return s
}

type class int

const (
	keep class = iota
	newline
	space
	drop
)

// classify decides what happens to one rune. Everything not printable, and
// every invisible or direction-changing character, is dropped: those are
// the characters an injection hides in and a reader never sees.
func classify(r rune) class {
	switch r {
	case '\n':
		return newline
	case '\t', ' ':
		return space
	case '\r':
		return drop
	case 0x2028, 0x2029, 0x85: // line and paragraph separators, NEL
		return newline
	}
	switch {
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f: // C0, DEL, C1
		return drop
	case r == 0xad, r == 0x34f: // soft hyphen, combining grapheme joiner
		return drop
	case r == 0x61c, r == 0x200e, r == 0x200f: // bidi marks
		return drop
	case r >= 0x200b && r <= 0x200d, r == 0x2060, r == 0xfeff: // zero-width
		return drop
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069: // bidi embeddings and isolates
		return drop
	case r >= 0xfff9 && r <= 0xfffb: // interlinear annotation
		return drop
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef: // variation selectors
		return drop
	case r >= 0xe0000 && r <= 0xe007f: // tag characters: invisible text
		return drop
	case unicode.Is(unicode.Co, r): // private use: icon fonts, not text
		return drop
	case unicode.Is(unicode.Zs, r): // every other space separator
		return space
	case unicode.Is(unicode.Cf, r): // any format character not named above
		return drop
	}
	return keep
}

// Wrap puts cleaned text between BeginMarker and EndMarker. source names
// where the text came from — an origin such as "https://example.com", never
// a full URL, so the header carries nothing the page chose. The header also
// carries the notices and whether the text was truncated, because a reader
// deciding how much to trust a passage needs those beside it, not in a
// separate field it may not have been shown.
func Wrap(c Cleaned, source string) string {
	var b strings.Builder
	b.WriteString(BeginMarker)
	b.WriteString(` source="`)
	b.WriteString(attr(source))
	b.WriteByte('"')
	if len(c.Notices) > 0 {
		b.WriteString(` notices="`)
		b.WriteString(strings.Join(c.Notices, ","))
		b.WriteByte('"')
	}
	if c.Truncated {
		b.WriteString(` truncated="true"`)
	}
	b.WriteString(">>>\n")
	b.WriteString(c.Text)
	b.WriteByte('\n')
	b.WriteString(EndMarker)
	return b.String()
}

// attr makes a string safe inside a quoted marker attribute: cleaned like
// any other text, one line, no quotes or brackets.
func attr(s string) string {
	s = Clean(s, 256).Text
	s = strings.NewReplacer("\"", "", "<", "", ">", "", "\n", " ").Replace(s)
	return s
}
