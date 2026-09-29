package web

import (
	"fmt"
	"net/netip"
	"strings"
)

// The SSRF floor. Not configurable, and listed in policy.example.yaml so a
// reviewer sees it: a deployment can narrow what fetch_page reaches, never
// widen it onto the tenant's own network. Hermes's floor is the model —
// RFC 1918, loopback, link-local, CGNAT, cloud metadata, reserved ranges —
// plus every range whose addresses embed or map another address, because
// an IPv6 address that "is" 10.0.0.1 is 10.0.0.1.

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
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// addrIsPublic says whether a is somewhere on the public internet. Every
// address a host resolves to must pass, and the address actually dialled
// is one that did.
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

// localNames never resolve to anything a fetch should reach, whatever DNS
// says: metadata endpoints, mDNS, Kubernetes service names, the reverse
// tree. Exact names, and suffixes led by a dot.
var localNames = []string{
	"localhost", ".localhost",
	".local", ".internal", ".home.arpa", ".arpa",
	"metadata", "instance-data", "metadata.google.internal",
	"kubernetes.default", ".svc", ".cluster.local",
}

// checkHostFloor refuses a host name that must never be fetched, before
// DNS is consulted. An IP literal is refused whatever its value: the
// allowlist names hosts, and a private literal gets the more useful
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
		return refusal("403", fmt.Sprintf("refused: %q is an IP address; the allowlist names hosts", host))
	}
	for _, n := range localNames {
		if host == n || (strings.HasPrefix(n, ".") && strings.HasSuffix(host, n)) || host == strings.TrimPrefix(n, ".") {
			return refusal("403", fmt.Sprintf("refused: %q is a local or metadata name", host))
		}
	}
	return nil
}
