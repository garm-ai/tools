package search

import (
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestNonPublicAddressesAreRefused(t *testing.T) {
	for _, s := range []string{
		"10.0.0.1", "172.16.5.5", "192.168.1.1", // RFC 1918
		"127.0.0.1", "0.0.0.0", // loopback, unspecified
		"169.254.169.254", // link-local: the cloud metadata endpoint
		"100.64.0.1",      // carrier-grade NAT
		"192.0.0.170", "198.18.0.1", "192.0.2.1", "198.51.100.1", "203.0.113.1",
		"240.0.0.1", "255.255.255.255", "224.0.0.1",
		"::1", "::", "fe80::1", "fc00::1", "fd12::1", "ff02::1",
		"::ffff:10.0.0.1", // IPv4-mapped private
		"64:ff9b::a00:1",  // NAT64-embedded private
		"2002:a00:1::",    // 6to4-embedded private
		"2001::1",         // Teredo
		"2001:db8::1",     // documentation
		"fd00:ec2::254",   // AWS IMDS over IPv6
		"fec0::1",         // site-local
		"::a00:1",         // IPv4-compatible: 10.0.0.1
		"::7f00:1",        // IPv4-compatible: loopback
		"::ffff:169.254.169.254",
	} {
		if addrIsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s was judged public", s)
		}
	}
}

func TestPublicAddressesArePublic(t *testing.T) {
	for _, s := range []string{"93.184.216.34", "1.1.1.1", "8.8.8.8", "2606:4700::1111"} {
		if !addrIsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s was judged non-public", s)
		}
	}
}

func TestLocalAndDisguisedHostsAreRefusedBeforeDNS(t *testing.T) {
	for _, host := range []string{
		"localhost", "foo.localhost", "printer.local", "db.internal",
		"router.home.arpa", "1.0.0.127.in-addr.arpa",
		"metadata", "instance-data", "metadata.google.internal",
		"kubernetes.default", "api.svc", "svc.cluster.local",
		// IP literals and every lenient spelling of one.
		"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1",
		"2130706433", "0x7f000001", "0177.0.0.1", "127.1",
		"", // no host at all
	} {
		if err := checkHostFloor(host); err == nil {
			t.Errorf("checkHostFloor(%q) allowed it", host)
		}
	}
}

func TestOrdinaryNamesPassTheFloor(t *testing.T) {
	for _, host := range []string{
		"www.gov.uk", "bankofengland.co.uk", "api.search.brave.com",
		"a-b.example.com", "xn--bcher-kva.example",
	} {
		if err := checkHostFloor(host); err != nil {
			t.Errorf("checkHostFloor(%q) = %v", host, err)
		}
	}
}

// The egress half. An endpoint is the one thing this service connects to,
// and it connects to it carrying a credential, so the checks are tighter
// than the fetcher's — a query string of the operator's own is refused
// because that is where an API key gets pasted.
func TestTheEndpointFloorRefusesWhatWouldLeakTheKey(t *testing.T) {
	for _, tc := range []struct{ endpoint, want string }{
		{"http://api.search.brave.com/x", "not https"},
		{"https://user:pw@api.search.brave.com/x", "credentials"},
		{"https://api.search.brave.com/x?key=sk-abc123", "query string"},
		{"https://api.search.brave.com/x#frag", "fragment"},
		{"https://127.0.0.1/x", "not a public address"},
		{"https://169.254.169.254/latest/meta-data/", "not a public address"},
		{"https://localhost/x", "local or metadata name"},
		{"https://metadata.google.internal/x", "local or metadata name"},
		{"https://2130706433/x", "IP address"},
	} {
		_, err := checkEndpoint(tc.endpoint)
		if err == nil {
			t.Errorf("checkEndpoint(%q) allowed it", tc.endpoint)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("checkEndpoint(%q) = %q, want it to mention %q", tc.endpoint, err, tc.want)
		}
	}
	if _, err := checkEndpoint(braveEndpoint); err != nil {
		t.Errorf("checkEndpoint(%q) = %v", braveEndpoint, err)
	}
}

// The ingress half. Nothing here is connected to; these URLs are about to
// be handed to a model, which will try to fetch them.
func TestTheResultFloorRefusesLinksNobodyShouldBeShown(t *testing.T) {
	for _, raw := range []string{
		"http://www.gov.uk/x",                                    // fetch_page fetches https only
		"ftp://www.gov.uk/x",                                     // not a web scheme at all
		"https://user:pw@www.gov.uk/x",                           // credentials
		"https://127.0.0.1/x",                                    // loopback
		"https://10.1.2.3/x",                                     // RFC 1918
		"https://169.254.169.254/x",                              // cloud metadata
		"https://[::1]/x",                                        // loopback, v6
		"https://[::ffff:10.0.0.1]/x",                            // mapped private
		"https://metadata.google.internal",                       // metadata by name
		"https://kubernetes.default/x",                           // a service inside the cluster
		"https://0x7f000001/x",                                   // a loopback literal the resolver would accept
		"https://" + strings.Repeat("a.", 200) + "example.com/x", // not a name DNS could carry
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if err := checkResultURL(u); err == nil {
			t.Errorf("checkResultURL(%q) allowed it", raw)
		}
	}
	for _, raw := range []string{
		"https://www.gov.uk/guidance/one",
		"https://bankofengland.co.uk/two?q=1#x",
	} {
		u, _ := url.Parse(raw)
		if err := checkResultURL(u); err != nil {
			t.Errorf("checkResultURL(%q) = %v", raw, err)
		}
	}
}
