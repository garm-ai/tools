package search

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The safety floor. Not configurable, and listed in policy.example.yaml so
// a reviewer sees it: a deployment can narrow what search_web reaches and
// what it hands back, never widen either onto the tenant's own network.
//
// A search client's floor is NOT a fetcher's, because it has two sides.
//
//  1. EGRESS, to one endpoint the operator configured. A credential is
//     attached to that request, so the floor is tighter than the fetcher's
//     rather than looser: https only, no credentials in the URL, no query
//     string of the operator's own, the host floor by name before DNS, every
//     resolved address checked, and NO REDIRECTS AT ALL. fetch_page follows
//     up to five hops because a page legitimately moves; a search API does
//     not, and a followed hop is how a poisoned DNS answer or a compromised
//     endpoint walks this deployment's API key to another host.
//
//  2. INGRESS, from the index. Nothing here connects to a result URL, so
//     there is no SSRF in it — but every result is a string written outside
//     the tenant that is about to be read by a model, and a model that is
//     shown a link tends to fetch it. So the same address floor and the same
//     host policy are applied to the URLs returned, and a result that fails
//     is DROPPED rather than refused: one bad link in an index's answer is
//     not a reason to fail the call, and the count of what was dropped is
//     reported so nobody is misled about how much was found.
//
// The address list is Hermes's floor — RFC 1918, loopback, link-local,
// CGNAT, cloud metadata, reserved ranges — plus every range whose addresses
// embed or map another address, because an IPv6 address that "is" 10.0.0.1
// is 10.0.0.1.

var nonPublicPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",          // this network
		"100.64.0.0/10",      // carrier-grade NAT
		"192.0.0.0/24",       // IETF protocol assignments
		"192.0.2.0/24",       // documentation
		"198.18.0.0/15",      // benchmarking
		"198.51.100.0/24",    // documentation
		"203.0.113.0/24",     // documentation
		"240.0.0.0/4",        // reserved
		"255.255.255.255/32", // broadcast
		"64:ff9b::/96",       // NAT64: embeds an IPv4 address
		"64:ff9b:1::/48",     // local-use NAT64
		"100::/64",           // discard
		"2001::/32",          // Teredo: embeds an IPv4 address
		"2001:db8::/32",      // documentation
		"2002::/16",          // 6to4: embeds an IPv4 address
		"fec0::/10",          // site-local (deprecated, still routed as unicast)
		"::/96",              // IPv4-compatible (deprecated): embeds an IPv4 address
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// addrIsPublic says whether a is somewhere on the public internet. Every
// address the endpoint's host resolves to must pass, the address actually
// dialled is one that did, and a result URL that is an address at all must
// pass before it is shown to anyone.
func addrIsPublic(a netip.Addr) bool {
	a = a.Unmap()
	switch {
	case !a.IsValid(), a.IsUnspecified(), a.IsLoopback(), a.IsPrivate(),
		a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast(),
		a.IsInterfaceLocalMulticast(), a.IsMulticast():
		return false
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// localNames never name anything a search should reach or recommend,
// whatever DNS says: metadata endpoints, mDNS, Kubernetes service names,
// the reverse tree. Exact names, and suffixes led by a dot.
var localNames = []string{
	"localhost", ".localhost",
	".local", ".internal", ".home.arpa", ".arpa",
	"metadata", "instance-data", "metadata.google.internal",
	"kubernetes.default", ".svc", ".cluster.local",
}

// checkHostFloor refuses a host that must never be dialled or recommended,
// before DNS is consulted. An IP literal is refused whatever its value: a
// host policy names hosts, and a private literal gets the more useful
// message.
func checkHostFloor(host string) error {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return refusal("400", "refused: the URL has no host")
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if !addrIsPublic(a) {
			return refusal("403", fmt.Sprintf("refused: %q is not a public address", host))
		}
		return refusal("403", fmt.Sprintf("refused: %q is an IP address; the host policy names hosts", host))
	}
	// A disguised IPv4 literal — 2130706433, 0x7f000001, 0177.0.0.1, 127.1 —
	// is not an address to netip but is one to the platform resolver, which
	// parses leniently. The WHATWG rule: a host whose last label is all
	// digits or hex-prefixed is a number, and a number is not a name.
	if endsInANumber(host) {
		return refusal("403", fmt.Sprintf("refused: %q is an IP address; the host policy names hosts", host))
	}
	for _, n := range localNames {
		if host == n || (strings.HasPrefix(n, ".") && strings.HasSuffix(host, n)) || host == strings.TrimPrefix(n, ".") {
			return refusal("403", fmt.Sprintf("refused: %q is a local or metadata name", host))
		}
	}
	return nil
}

// endsInANumber is the WHATWG URL "ends in a number" check: the last
// non-empty label is decimal digits, or 0x followed by hex digits.
func endsInANumber(host string) bool {
	labels := strings.Split(host, ".")
	last := labels[len(labels)-1]
	if last == "" && len(labels) > 1 {
		last = labels[len(labels)-2]
	}
	if last == "" {
		return false
	}
	if rest, ok := strings.CutPrefix(last, "0x"); ok {
		if rest == "" {
			return true
		}
		for _, r := range rest {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				return false
			}
		}
		return true
	}
	for _, r := range last {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// maxHostRunes is the longest name DNS can carry.
const maxHostRunes = 253

// hostLabelsRE is the shape of a DNS name as every check here compares it,
// as a string so policy.go can build its own patterns from it.
const hostLabelsRE = `[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*`

// Resolver is what guardedDial asks. *net.Resolver satisfies it; tests
// substitute a map.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// DialFunc opens the TCP connection once the address has been vetted. The
// default is a net.Dialer; tests substitute one that connects to a local
// server whatever address it is given, and record what it was given.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// guardedDial is the endpoint transport's DialContext: resolve the host
// here, refuse unless every address is public, then dial the vetted address
// rather than letting the transport resolve again. Resolving twice is how a
// rebinding attack passes a check with one answer and connects with
// another — and this connection carries the deployment's API key.
func guardedDial(resolve Resolver, raw DialFunc) DialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, refusal("400", "refused: malformed address")
		}
		host = strings.TrimSuffix(strings.ToLower(host), ".")
		if err := checkHostFloor(host); err != nil {
			return nil, err
		}
		addrs, err := resolve.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addrs) == 0 {
			// Treated as blocked, as Hermes does: a name that does not
			// resolve is not evidence of anything, and nothing is sent.
			return nil, refusal("502", fmt.Sprintf("upstream: %q did not resolve", host))
		}
		for _, a := range addrs {
			if !addrIsPublic(a) {
				return nil, refusal("403", fmt.Sprintf("refused: %q resolved to a non-public address", host))
			}
		}
		return raw(ctx, network, net.JoinHostPort(addrs[0].Unmap().String(), port))
	}
}

// checkEndpoint is the egress half of the floor, run once at boot so an
// endpoint that could never be used stops the service where an operator is
// present rather than failing every call later.
//
// No credentials, no query and no fragment: the client builds the whole
// query itself, and an operator who pastes an API key into the endpoint
// URL — the single most common way a search key ends up in a proxy log —
// is told at boot instead of leaking one request at a time.
func checkEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("endpoint: %q could not be parsed", raw)
	}
	switch {
	case u.Scheme != "https":
		return nil, fmt.Errorf("endpoint: scheme %q is not https; the API key would travel in clear", u.Scheme)
	case u.User != nil:
		return nil, fmt.Errorf("endpoint: the URL carries credentials; use --api-key-file")
	case u.RawQuery != "":
		return nil, fmt.Errorf("endpoint: the URL carries a query string; the client builds the query, and a key pasted into one would be logged by every proxy in the path")
	case u.Fragment != "":
		return nil, fmt.Errorf("endpoint: the URL carries a fragment")
	}
	host := hostOf(u)
	if err := checkHostFloor(host); err != nil {
		return nil, fmt.Errorf("endpoint: %w", err)
	}
	return u, nil
}

// checkResultURL is the ingress half: what a URL the index chose must be
// before this service will repeat it to a model. https, because fetch_page
// fetches nothing else and a link an agent cannot follow is a link that
// only invites it to try; no credentials; a host DNS could carry; and past
// the address floor. The host POLICY is applied separately, by the caller,
// because its refusal names the rule that matched.
func checkResultURL(u *url.URL) error {
	if u.Scheme != "https" {
		return refusal("403", "dropped: not an https URL")
	}
	if u.User != nil {
		return refusal("403", "dropped: the URL carries credentials")
	}
	host := hostOf(u)
	// url.Parse accepts `<`, `>`, `"` and raw UTF-8 in a host. A host with
	// those is not a host, and repeating one to a model is repeating text
	// the index chose in a field the model reads as an address.
	if utf8.RuneCountInString(host) > maxHostRunes || !hostName.MatchString(host) {
		return refusal("403", "dropped: the host is not a name DNS could carry")
	}
	return checkHostFloor(host)
}

// hostName is the shape a host itself has: lower-case DNS labels, no
// wildcard. An IP literal does not match and is refused by the floor,
// which names it.
var hostName = regexp.MustCompile(`^` + hostLabelsRE + `$`)

// hostOf normalises a URL's host the way every check here compares it.
func hostOf(u *url.URL) string {
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

// origin is scheme://host[:port]: what a caller who may not read a URL's
// path is still shown of it, and what a snippet's wrapper names as its
// source.
func origin(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}
