package web

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/garm-ai/tool-go/toolbind"
)

func parse(t *testing.T, text string) (*Policy, error) {
	t.Helper()
	return ParsePolicy(strings.NewReader(text))
}

func mustParse(t *testing.T, text string) *Policy {
	t.Helper()
	p, err := parse(t, text)
	if err != nil {
		t.Fatalf("parsing %q: %v", text, err)
	}
	return p
}

// codeOf reads the code and message a caller would see off an error, the
// way garmtool does: through errors.As, so wrapping does not hide it.
func codeOf(t *testing.T, err error) (string, string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	var coded toolbind.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	return coded.Code, coded.Message
}

func TestAPolicyWithoutAnAllowlistDoesNotLoad(t *testing.T) {
	for _, text := range []string{
		"",
		"block:\n  - evil.example\n",
		"allow: []\n",
	} {
		if p, err := parse(t, text); err == nil {
			t.Errorf("%q loaded as %+v; a policy that allows nothing must not start the service", text, p)
		} else if !strings.Contains(err.Error(), "allowlist") {
			t.Errorf("%q: error %q does not say the allowlist is missing", text, err)
		}
	}
}

func TestAnUnknownKeyIsALoadErrorNotAnEmptyAllowlist(t *testing.T) {
	_, err := parse(t, "alow:\n  - example.com\n")
	if err == nil {
		t.Fatal("a misspelled allow: key loaded")
	}
	if !strings.Contains(err.Error(), "alow") {
		t.Errorf("the error does not name the unknown key: %v", err)
	}
}

func TestMalformedYAMLDoesNotLoad(t *testing.T) {
	if _, err := parse(t, "allow: [\n"); err == nil {
		t.Fatal("malformed YAML loaded")
	}
}

func TestRulesMustBeHostsOrWildcards(t *testing.T) {
	for _, bad := range []string{"https://example.com", "example.com:443", "*example.com", "example.com/path", "-bad.example", "*.*.com"} {
		if _, err := parse(t, "allow:\n  - \""+bad+"\"\n"); err == nil {
			t.Errorf("rule %q was accepted; it would silently match nothing", bad)
		}
	}
	for _, good := range []string{"example.com", "*.example.com", "*.uk", "a-b.c-d.example", "xn--bcher-kva.example"} {
		if _, err := parse(t, "allow:\n  - \""+good+"\"\n"); err != nil {
			t.Errorf("rule %q was refused: %v", good, err)
		}
	}
}

func TestBlockWinsOverAllowAndNamesItsRule(t *testing.T) {
	p := mustParse(t, "allow:\n  - \"*.example.com\"\nblock:\n  - blocked.example.com\n")
	for _, host := range []string{"blocked.example.com", "BLOCKED.example.com", "blocked.example.com."} {
		code, msg := codeOf(t, p.CheckHost(host))
		if code != "403" {
			t.Errorf("%s: code %s, want 403", host, code)
		}
		if !strings.Contains(msg, `block rule "blocked.example.com"`) {
			t.Errorf("%s: %q does not name the rule that matched", host, msg)
		}
	}
	if err := p.CheckHost("www.example.com"); err != nil {
		t.Errorf("an allowed host was refused: %v", err)
	}
}

func TestWildcardsMatchSubdomainsNotTheApex(t *testing.T) {
	p := mustParse(t, "allow:\n  - \"*.gov.uk\"\n")
	for _, ok := range []string{"www.gov.uk", "a.b.gov.uk"} {
		if err := p.CheckHost(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, no := range []string{"gov.uk", "notgov.uk", "gov.uk.evil.example"} {
		if _, msg := codeOf(t, p.CheckHost(no)); !strings.Contains(msg, "not on the allowlist") {
			t.Errorf("%s: %q", no, msg)
		}
	}
}

func TestExactRulesMatchExactly(t *testing.T) {
	p := mustParse(t, "allow:\n  - bankofengland.co.uk\n")
	if err := p.CheckHost("bankofengland.co.uk"); err != nil {
		t.Errorf("refused: %v", err)
	}
	if err := p.CheckHost("www.bankofengland.co.uk"); err == nil {
		t.Error("an exact rule matched a subdomain")
	}
}

func TestAZeroPolicyRefusesEverything(t *testing.T) {
	for _, p := range []*Policy{nil, {}, {Block: []string{"x.example"}}} {
		code, msg := codeOf(t, p.CheckHost("example.com"))
		if code != "403" || !strings.Contains(msg, "no allowlist") {
			t.Errorf("%+v: %s %q", p, code, msg)
		}
	}
}

func TestDefaultsFillUnsetCaps(t *testing.T) {
	p := mustParse(t, "allow:\n  - example.com\n")
	if p.MaxRedirects != DefaultMaxRedirects || p.MaxBodyBytes != DefaultMaxBodyBytes ||
		p.MaxChars != DefaultMaxChars || p.Timeout != DefaultTimeout || p.UserAgent != DefaultUserAgent {
		t.Errorf("defaults not applied: %+v", p)
	}
	p = mustParse(t, "allow:\n  - example.com\nmax_redirects: 2\nmax_body_bytes: 1024\nmax_chars: 4000\ntimeout: 3s\nuser_agent: test/1\n")
	if p.MaxRedirects != 2 || p.MaxBodyBytes != 1024 || p.MaxChars != 4000 || p.Timeout != 3*time.Second || p.UserAgent != "test/1" {
		t.Errorf("explicit values not kept: %+v", p)
	}
}

func TestCapsOutOfRangeDoNotLoad(t *testing.T) {
	for _, text := range []string{
		"allow: [example.com]\nmax_chars: 100\n",
		"allow: [example.com]\nmax_chars: 300000\n",
		"allow: [example.com]\nmax_redirects: -1\n",
		"allow: [example.com]\nmax_body_bytes: -1\n",
		"allow: [example.com]\ntimeout: -1s\n",
	} {
		if _, err := parse(t, text); err == nil {
			t.Errorf("%q loaded", text)
		}
	}
}

func TestDigestIsOverTheListsAndOrderIndependent(t *testing.T) {
	a := mustParse(t, "allow: [a.example, b.example]\nblock: [c.example]\n")
	b := mustParse(t, "allow: [b.example, a.example]\nblock: [c.example]\ntimeout: 1s\n")
	c := mustParse(t, "allow: [a.example, b.example]\nblock: [c.example, d.example]\n")
	if a.Digest() != b.Digest() {
		t.Errorf("order or an unrelated cap changed the digest: %s vs %s", a.Digest(), b.Digest())
	}
	if a.Digest() == c.Digest() {
		t.Error("adding a block rule did not change the digest")
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(a.Digest()) {
		t.Errorf("digest %q is not lowercase hex sha256", a.Digest())
	}
}
