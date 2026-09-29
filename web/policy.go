// Package web serves fetch_page: one public https page in, its readable
// text out, wrapped as untrusted.
//
// The service holds the policy of record — which hosts may be fetched at
// all — and an agent's manifest narrows it with a CEL guard over args.url.
// Nothing here decides who may call: garmd did that before the request
// arrived. Every limit in this package is a SAFETY limit (hosts, addresses,
// bytes, seconds), never an authorisation one.
package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/garm-ai/tool-go/toolbind"
)

// Defaults for the caps a policy file may leave unset. Hermes's numbers,
// which have held up in use, except the user agent.
const (
	DefaultMaxRedirects = 5
	DefaultMaxBodyBytes = 5 << 20
	DefaultMaxChars     = 15000
	MinChars            = 2000
	MaxChars            = 200000
	DefaultTimeout      = 20 * time.Second
	DefaultUserAgent    = "garm-fetch/0.1"
)

// Policy is the service configuration: the allow and block lists and the
// caps. It is loaded once at boot from a YAML file and never changes while
// the process runs.
//
// There is no "blocklist mode". A policy with no allowlist fetches nothing:
// that is what fail-closed means here, and it is the inverse of Hermes's
// check_website_access, which fails open on a malformed policy so a typo
// cannot break every web tool. A typo here breaks every fetch, on purpose,
// at boot, where the operator is present.
type Policy struct {
	// Allow lists the hosts this deployment may fetch: an exact host
	// ("bankofengland.co.uk") or a wildcard over subdomains ("*.gov.uk",
	// which matches www.gov.uk and a.b.gov.uk but not gov.uk itself).
	Allow []string `yaml:"allow"`
	// Block is checked first and wins over Allow. Same syntax.
	Block []string `yaml:"block"`

	MaxRedirects int           `yaml:"max_redirects"`
	MaxBodyBytes int64         `yaml:"max_body_bytes"`
	MaxChars     int           `yaml:"max_chars"`
	Timeout      time.Duration `yaml:"timeout"`
	UserAgent    string        `yaml:"user_agent"`

	digest string
}

// hostRule is what an allow or block entry may look like: lower-case DNS
// labels, optionally led by "*.". No scheme, no port, no path — an entry
// that carried one would silently match nothing.
var hostRule = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// LoadPolicy reads a policy file. Any problem is an error and the caller
// must not serve: a service that starts on a policy it could not read is
// a service that fetches whatever it likes.
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
// `alow:` must not load as an empty allowlist and then refuse every fetch
// with a message about the allowlist being empty.
func ParsePolicy(r io.Reader) (*Policy, error) {
	var p Policy
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the policy file is empty; an allowlist is required")
		}
		return nil, err
	}
	// One document. A second one would load silently as nothing, and the
	// operator who wrote it would believe its rules apply.
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("the policy file has more than one YAML document; one is expected")
	}
	if len(p.Allow) == 0 {
		return nil, errors.New("the policy allows no hosts; an allowlist is required, because this service fails closed")
	}
	for i, rule := range p.Allow {
		p.Allow[i] = strings.ToLower(strings.TrimSpace(rule))
		if !hostRule.MatchString(p.Allow[i]) {
			return nil, fmt.Errorf("allow[%d] %q is not a host or a *.host pattern", i, rule)
		}
		// An allow entry the floor would refuse anyway (an IP literal, a
		// local or metadata name) can never match a fetch; loading it
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
	case p.MaxRedirects < 0:
		return nil, errors.New("max_redirects must not be negative")
	case p.MaxRedirects == 0:
		p.MaxRedirects = DefaultMaxRedirects
	}
	switch {
	case p.MaxBodyBytes < 0:
		return nil, errors.New("max_body_bytes must not be negative")
	case p.MaxBodyBytes == 0:
		p.MaxBodyBytes = DefaultMaxBodyBytes
	}
	switch {
	case p.MaxChars < 0 || (p.MaxChars > 0 && p.MaxChars < MinChars):
		return nil, fmt.Errorf("max_chars must be at least %d", MinChars)
	case p.MaxChars > MaxChars:
		return nil, fmt.Errorf("max_chars must be at most %d", MaxChars)
	case p.MaxChars == 0:
		p.MaxChars = DefaultMaxChars
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
	p.digest = digestOf(p.Allow, p.Block)
	return &p, nil
}

// Digest identifies the lists in force: sha256, lowercase hex, over the
// sorted allow and block entries. Returned on every response so a ledger
// row can be joined to the exact policy that permitted a fetch.
func (p *Policy) Digest() string {
	if p == nil {
		return ""
	}
	if p.digest == "" {
		p.digest = digestOf(p.Allow, p.Block)
	}
	return p.digest
}

func digestOf(allow, block []string) string {
	a := append([]string(nil), allow...)
	b := append([]string(nil), block...)
	sort.Strings(a)
	sort.Strings(b)
	h := sha256.New()
	h.Write([]byte("allow:\n" + strings.Join(a, "\n") + "\nblock:\n" + strings.Join(b, "\n") + "\n"))
	return hex.EncodeToString(h.Sum(nil))
}

// CheckHost answers whether host may be fetched under this policy. Block
// first, then allow; a nil or empty policy refuses everything. The error
// names the rule that matched, which the tool's on_error guidance promises.
func (p *Policy) CheckHost(host string) error {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if p == nil || len(p.Allow) == 0 {
		return refusal("403", "refused: no allowlist is configured; nothing is fetched")
	}
	for _, rule := range p.Block {
		if matchRule(rule, host) {
			return refusal("403", fmt.Sprintf("refused: host %q matches block rule %q", host, rule))
		}
	}
	for _, rule := range p.Allow {
		if matchRule(rule, host) {
			return nil
		}
	}
	return refusal("403", fmt.Sprintf("refused: host %q is not on the allowlist", host))
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
// (the host it asked for, the rule that matched) and never page content.
func refusal(code, message string) error {
	return toolbind.CodedError{Code: code, Message: message}
}
