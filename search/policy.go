// Package search serves search_web: one query in, a short list of links
// and excerpts out, each excerpt wrapped as untrusted.
//
// The service holds the policy of record — which endpoint is asked, and
// which hosts may appear in an answer — and an agent's manifest narrows it
// with a CEL guard over args.query and args.limit. Nothing here decides who
// may call: garmd did that before the request arrived. Every limit in this
// package is a SAFETY limit (hosts, addresses, results, characters,
// seconds), never an authorisation one.
package search

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/garm-ai/tool-go/toolbind"
)

// Defaults for the caps a policy file may leave unset. Hermes's numbers
// where it has one, and the vendor's where the vendor sets it.
const (
	// DefaultLimit is Hermes's web_search default.
	DefaultLimit = 5
	// MaxLimit is the most one upstream page carries, and the ceiling
	// buf.validate puts on the request field.
	MaxLimit = 20
	// DefaultMaxSnippetChars caps one excerpt. An index writes two or three
	// lines; anything much longer is a page, and a page is fetch_page's job.
	DefaultMaxSnippetChars = 1000
	MinSnippetChars        = 100
	MaxSnippetChars        = 4000
	// MaxTitleRunes caps a title, which is one line of a result and not a
	// second snippet.
	MaxTitleRunes = 200
	// MaxURLRunes is the cap fetch_page puts on a URL it will accept. A
	// result longer than that is a link nothing here could follow.
	MaxURLRunes = 2048

	DefaultTimeout   = 10 * time.Second
	DefaultUserAgent = "garm-search/0.1"
	// DefaultMaxBodyBytes caps the JSON the endpoint may send back. Twenty
	// results with a snippet each is tens of kilobytes; two megabytes is
	// room for every optional field a vendor might add and still a bound.
	DefaultMaxBodyBytes = 2 << 20
)

// BackendBrave is the only backend implemented. Named in the policy file
// so adding a second one is a policy change and not a redeployment onto a
// different binary.
const BackendBrave = "brave"

// Policy is the service configuration: which endpoint to ask, which hosts
// may appear in an answer, and the caps. It is loaded once at boot from a
// YAML file and never changes while the process runs.
//
// There is no "blocklist mode". A policy with no allowlist returns nothing:
// that is what fail-closed means here, and it is the inverse of Hermes's
// check_website_access, which fails open on a malformed policy so a typo
// cannot break every web tool. A typo here returns no results at all, on
// purpose, at boot, where the operator is present.
type Policy struct {
	// Backend names the search API this deployment talks to. "brave" is
	// the only value implemented.
	Backend string `yaml:"backend"`
	// Endpoint is the full URL of that API's search method. https, with no
	// credentials and no query of its own.
	Endpoint string `yaml:"endpoint"`

	// Allow lists the hosts whose links may be returned: an exact host
	// ("bankofengland.co.uk") or a wildcard over subdomains ("*.gov.uk",
	// which matches www.gov.uk and a.b.gov.uk but not gov.uk itself). A
	// result on any other host is dropped, so an agent is never shown a
	// link fetch_page would refuse.
	Allow []string `yaml:"allow"`
	// Block is checked first and wins over Allow. Same syntax.
	Block []string `yaml:"block"`

	MaxResults      int           `yaml:"max_results"`
	MaxSnippetChars int           `yaml:"max_snippet_chars"`
	MaxBodyBytes    int64         `yaml:"max_body_bytes"`
	Timeout         time.Duration `yaml:"timeout"`
	UserAgent       string        `yaml:"user_agent"`
	endpointURL     *url.URL
	digest          string
}

// hostRule is what an allow or block entry may look like: lower-case DNS
// labels, optionally led by "*.". No scheme, no port, no path — an entry
// that carried one would silently match nothing.
var hostRule = regexp.MustCompile(`^(\*\.)?` + hostLabelsRE + `$`)

// LoadPolicy reads a policy file. Any problem is an error and the caller
// must not serve: a service that starts on a policy it could not read is a
// service that repeats whatever links it is handed.
func LoadPolicy(path string) (*Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	defer f.Close()
	p, err := ParsePolicy(f)
	if err != nil {
		return nil, fmt.Errorf("policy %s: %w", path, err)
	}
	return p, nil
}

// ParsePolicy decodes and validates a policy. Unknown keys are an error:
// `alow:` must not load as an empty allowlist and then drop every result
// with a message about the allowlist being empty.
func ParsePolicy(r io.Reader) (*Policy, error) {
	var p Policy
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the policy file is empty; an endpoint and an allowlist are required")
		}
		return nil, err
	}
	// One document. A second one would load silently as nothing, and the
	// operator who wrote it would believe its rules apply.
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("the policy file has more than one YAML document; one is expected")
	}

	switch p.Backend {
	case "":
		return nil, fmt.Errorf("backend is required; the only one implemented is %q", BackendBrave)
	case BackendBrave:
	default:
		return nil, fmt.Errorf("backend %q is not implemented; the only one is %q", p.Backend, BackendBrave)
	}
	if p.Endpoint == "" {
		p.Endpoint = braveEndpoint
	}
	u, err := checkEndpoint(p.Endpoint)
	if err != nil {
		return nil, err
	}
	p.endpointURL = u

	if len(p.Allow) == 0 {
		return nil, errors.New("the policy allows no hosts; an allowlist is required, because this service fails closed")
	}
	for i, rule := range p.Allow {
		p.Allow[i] = strings.ToLower(strings.TrimSpace(rule))
		if !hostRule.MatchString(p.Allow[i]) {
			return nil, fmt.Errorf("allow[%d] %q is not a host or a *.host pattern", i, rule)
		}
		// An allow entry the floor would drop anyway (an IP literal, a
		// local or metadata name) can never match a result; loading it
		// would let an operator believe it does.
		if err := checkHostFloor(strings.TrimPrefix(p.Allow[i], "*.")); err != nil {
			return nil, fmt.Errorf("allow[%d] %q can never match: %v", i, rule, err)
		}
	}
	for i, rule := range p.Block {
		p.Block[i] = strings.ToLower(strings.TrimSpace(rule))
		if !hostRule.MatchString(p.Block[i]) {
			return nil, fmt.Errorf("block[%d] %q is not a host or a *.host pattern", i, rule)
		}
	}

	switch {
	case p.MaxResults < 0:
		return nil, errors.New("max_results must not be negative")
	case p.MaxResults > MaxLimit:
		return nil, fmt.Errorf("max_results must be at most %d, which is the most one upstream page carries", MaxLimit)
	case p.MaxResults == 0:
		p.MaxResults = MaxLimit
	}
	switch {
	case p.MaxSnippetChars < 0 || (p.MaxSnippetChars > 0 && p.MaxSnippetChars < MinSnippetChars):
		return nil, fmt.Errorf("max_snippet_chars must be at least %d", MinSnippetChars)
	case p.MaxSnippetChars > MaxSnippetChars:
		return nil, fmt.Errorf("max_snippet_chars must be at most %d", MaxSnippetChars)
	case p.MaxSnippetChars == 0:
		p.MaxSnippetChars = DefaultMaxSnippetChars
	}
	switch {
	case p.MaxBodyBytes < 0:
		return nil, errors.New("max_body_bytes must not be negative")
	case p.MaxBodyBytes == 0:
		p.MaxBodyBytes = DefaultMaxBodyBytes
	}
	switch {
	case p.Timeout < 0:
		return nil, errors.New("timeout must not be negative")
	case p.Timeout == 0:
		p.Timeout = DefaultTimeout
	}
	if p.UserAgent == "" {
		p.UserAgent = DefaultUserAgent
	}
	p.digest = digestOf(p.Backend, p.Endpoint, p.Allow, p.Block)
	return &p, nil
}

// EndpointURL is the vetted endpoint. Never nil on a policy ParsePolicy
// returned.
func (p *Policy) EndpointURL() *url.URL { return p.endpointURL }

// Digest identifies the policy in force: sha256, lowercase hex, over the
// backend, the endpoint and the sorted allow and block entries. Returned on
// every response so a ledger row can be joined to the exact policy that
// permitted these links. The API key is not part of it and never goes near
// a hash that is published.
func (p *Policy) Digest() string {
	if p == nil {
		return ""
	}
	if p.digest == "" {
		p.digest = digestOf(p.Backend, p.Endpoint, p.Allow, p.Block)
	}
	return p.digest
}

func digestOf(backend, endpoint string, allow, block []string) string {
	a := append([]string(nil), allow...)
	b := append([]string(nil), block...)
	sort.Strings(a)
	sort.Strings(b)
	h := sha256.New()
	h.Write([]byte("backend:" + backend + "\nendpoint:" + endpoint +
		"\nallow:\n" + strings.Join(a, "\n") + "\nblock:\n" + strings.Join(b, "\n") + "\n"))
	return hex.EncodeToString(h.Sum(nil))
}

// CheckHost answers whether a result on host may be returned. Block first,
// then allow; a nil or empty policy returns nothing. The error names the
// rule that matched, which the tool's on_error guidance promises — and it
// is a reason to DROP one result, not to refuse the call.
func (p *Policy) CheckHost(host string) error {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if p == nil || len(p.Allow) == 0 {
		return refusal("403", "dropped: no allowlist is configured; nothing is returned")
	}
	for _, rule := range p.Block {
		if matchRule(rule, host) {
			return refusal("403", fmt.Sprintf("dropped: host %q matches block rule %q", host, rule))
		}
	}
	for _, rule := range p.Allow {
		if matchRule(rule, host) {
			return nil
		}
	}
	return refusal("403", fmt.Sprintf("dropped: host %q is not on the allowlist", host))
}

// matchRule: "*.x" matches any proper subdomain of x; anything else matches
// exactly. Case and a trailing dot were normalised by the caller.
func matchRule(rule, host string) bool {
	if suffix, ok := strings.CutPrefix(rule, "*"); ok {
		return len(host) > len(suffix) && strings.HasSuffix(host, suffix)
	}
	return host == rule
}

// refusal is the one way this package names an error code. The message is
// published verbatim to the caller, so it carries the caller's own input
// (the limit it asked for) or a host the index chose, and never a snippet,
// a title, or one byte of the endpoint's answer.
func refusal(code, message string) error {
	return toolbind.CodedError{Code: code, Message: message}
}
