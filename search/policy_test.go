package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func parse(t *testing.T, yaml string) (*Policy, error) {
	t.Helper()
	return ParsePolicy(strings.NewReader(yaml))
}

const minimal = `
backend: brave
allow:
  - www.gov.uk
`

func TestAPolicyFillsItsDefaults(t *testing.T) {
	p, err := parse(t, minimal)
	if err != nil {
		t.Fatal(err)
	}
	if p.Endpoint != braveEndpoint {
		t.Errorf("endpoint = %q, want the backend's own", p.Endpoint)
	}
	if p.EndpointURL() == nil {
		t.Fatal("the endpoint was not vetted")
	}
	if p.MaxResults != MaxLimit || p.MaxSnippetChars != DefaultMaxSnippetChars ||
		p.MaxBodyBytes != DefaultMaxBodyBytes || p.Timeout != DefaultTimeout || p.UserAgent != DefaultUserAgent {
		t.Errorf("defaults were not filled: %+v", p)
	}
}

func TestAPolicyThatCouldNotBeMeantIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, yaml, want string }{
		{"empty", "", "empty"},
		{"no backend", "allow: [www.gov.uk]\n", "backend is required"},
		{"unknown backend", "backend: tavily\nallow: [www.gov.uk]\n", "not implemented"},
		{"no allowlist", "backend: brave\n", "allowlist is required"},
		{"empty allowlist", "backend: brave\nallow: []\n", "allowlist is required"},
		{"typo in a key", "backend: brave\nalow: [www.gov.uk]\n", "field alow not found"},
		{"two documents", "backend: brave\nallow: [www.gov.uk]\n---\nallow: [evil.example]\n", "more than one YAML document"},
		{"a scheme in a rule", "backend: brave\nallow: [\"https://www.gov.uk\"]\n", "not a host"},
		{"a path in a rule", "backend: brave\nallow: [\"www.gov.uk/x\"]\n", "not a host"},
		{"a rule the floor would drop", "backend: brave\nallow: [localhost]\n", "can never match"},
		{"an address as a rule", "backend: brave\nallow: [\"10.0.0.1\"]\n", "can never match"},
		{"a bad block rule", "backend: brave\nallow: [www.gov.uk]\nblock: [\"*\"]\n", "not a host"},
		{"an http endpoint", "backend: brave\nendpoint: http://x.example/s\nallow: [www.gov.uk]\n", "not https"},
		{"a key in the endpoint", "backend: brave\nendpoint: \"https://x.example/s?token=abc\"\nallow: [www.gov.uk]\n", "query string"},
		{"max_results too high", "backend: brave\nallow: [www.gov.uk]\nmax_results: 50\n", "at most 20"},
		{"max_snippet_chars too low", "backend: brave\nallow: [www.gov.uk]\nmax_snippet_chars: 10\n", "at least 100"},
		{"max_snippet_chars too high", "backend: brave\nallow: [www.gov.uk]\nmax_snippet_chars: 99999\n", "at most 4000"},
		{"a negative timeout", "backend: brave\nallow: [www.gov.uk]\ntimeout: -1s\n", "must not be negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse(t, tc.yaml)
			if err == nil {
				t.Fatalf("accepted:\n%s", tc.yaml)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestCheckHostBlocksBeforeItAllows(t *testing.T) {
	p, err := parse(t, `
backend: brave
allow: ["*.gov.uk", bankofengland.co.uk]
block: [blocked.gov.uk, "*.dev.gov.uk"]
`)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"www.gov.uk", "a.b.gov.uk", "bankofengland.co.uk", "WWW.GOV.UK", "www.gov.uk."} {
		if err := p.CheckHost(host); err != nil {
			t.Errorf("CheckHost(%q) = %v", host, err)
		}
	}
	for _, tc := range []struct{ host, want string }{
		{"gov.uk", "not on the allowlist"},                // *.gov.uk is subdomains, not the apex
		{"blocked.gov.uk", `block rule "blocked.gov.uk"`}, // block wins over *.gov.uk
		{"x.dev.gov.uk", `block rule "*.dev.gov.uk"`},
		{"evil.example", "not on the allowlist"},
		{"notgov.uk", "not on the allowlist"},
	} {
		err := p.CheckHost(tc.host)
		if err == nil {
			t.Errorf("CheckHost(%q) allowed it", tc.host)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("CheckHost(%q) = %q, want it to mention %q", tc.host, err, tc.want)
		}
	}
	// A nil policy is the fail-closed case, not a permissive one.
	if err := (*Policy)(nil).CheckHost("www.gov.uk"); err == nil {
		t.Error("a nil policy allowed a host")
	}
}

func TestTheDigestCoversWhatTheAnswerDependedOn(t *testing.T) {
	base, _ := parse(t, "backend: brave\nallow: [a.example, b.example]\n")
	// Order is not identity.
	same, _ := parse(t, "backend: brave\nallow: [b.example, a.example]\n")
	if base.Digest() != same.Digest() {
		t.Error("the digest depends on the order the operator wrote the list in")
	}
	for _, yaml := range []string{
		"backend: brave\nallow: [a.example]\n",
		"backend: brave\nallow: [a.example, b.example]\nblock: [c.example]\n",
		"backend: brave\nendpoint: https://other.example/s\nallow: [a.example, b.example]\n",
	} {
		other, err := parse(t, yaml)
		if err != nil {
			t.Fatal(err)
		}
		if other.Digest() == base.Digest() {
			t.Errorf("the digest did not move for:\n%s", yaml)
		}
	}
	// A timeout is not part of what a ledger row needs to be joined to: it
	// changes how long an answer took, not which links were permitted.
	slow, _ := parse(t, "backend: brave\nallow: [a.example, b.example]\ntimeout: 30s\n")
	if slow.Digest() != base.Digest() || slow.Timeout != 30*time.Second {
		t.Error("the digest moved for a timeout")
	}
}

// The API key is a file, and everything that is not exactly a key in it is
// a boot failure with the PATH in the message and nothing of the contents.
func TestTheAPIKeyComesFromAFileAndNeverAppearsInAnError(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}

	good := write("good", "  sk-secret-value\n", 0o600)
	key, err := LoadAPIKey(good)
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk-secret-value" {
		t.Errorf("key = %q; surrounding whitespace should be trimmed", key)
	}
	if w := KeyFileWarning(good); w != "" {
		t.Errorf("a 0600 key file warned: %q", w)
	}

	for _, tc := range []struct{ name, content, want string }{
		{"empty", "", "is empty"},
		{"whitespace only", "   \n\t\n", "is empty"},
		{"a whole export line", "export KEY=sk-secret-value", "whitespace or control characters"},
		{"a newline in the middle", "sk-secret\nvalue", "whitespace or control characters"},
		{"a header injection", "sk\r\nX-Other: 1", "whitespace or control characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := write(tc.name, tc.content, 0o600)
			_, err := LoadAPIKey(p)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
			// The one thing that must never be in it.
			if strings.Contains(err.Error(), "sk-secret") || strings.Contains(err.Error(), "sk\r") {
				t.Errorf("the error repeated the key file's contents: %q", err)
			}
		})
	}

	if _, err := LoadAPIKey(filepath.Join(dir, "absent")); err == nil {
		t.Error("a missing key file was accepted")
	}
	if _, err := LoadAPIKey(""); err == nil {
		t.Error("an empty path was accepted")
	}
	if w := KeyFileWarning(write("loose", "sk-x", 0o644)); !strings.Contains(w, "readable by more than its owner") {
		t.Errorf("a 0644 key file did not warn: %q", w)
	}
}
