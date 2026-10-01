package web

import (
	"net/netip"
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
	for _, s := range []string{"93.184.216.34", "8.8.8.8", "2606:4700::1111", "2a00:1450:4009::1"} {
		if !addrIsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s was judged non-public", s)
		}
	}
}

func TestLocalAndMetadataNamesAreRefusedBeforeDNS(t *testing.T) {
	for _, host := range []string{
		"localhost", "LOCALHOST", "foo.localhost", "localhost.",
		"metadata.google.internal", "metadata", "instance-data",
		"printer.local", "db.internal", "kubernetes.default", "api.svc", "x.cluster.local",
		"4.3.2.1.in-addr.arpa", "gateway.home.arpa",
	} {
		code, msg := codeOf(t, checkHostFloor(host))
		if code != "403" || !strings.Contains(msg, "local or metadata name") {
			t.Errorf("%s: %s %q", host, code, msg)
		}
	}
}

func TestIPLiteralsAreRefusedWhateverTheirValue(t *testing.T) {
	for host, want := range map[string]string{
		"10.0.0.1":        "is not a public address",
		"::1":             "is not a public address",
		"169.254.169.254": "is not a public address",
		"93.184.216.34":   "is an IP address; the allowlist names hosts",
	} {
		code, msg := codeOf(t, checkHostFloor(host))
		if code != "403" || !strings.Contains(msg, want) {
			t.Errorf("%s: %s %q, want %q", host, code, msg, want)
		}
	}
}

func TestAnEmptyHostIsRefused(t *testing.T) {
	if code, _ := codeOf(t, checkHostFloor("")); code != "400" {
		t.Errorf("code %s, want 400", code)
	}
}

func TestOrdinaryHostsPassTheFloor(t *testing.T) {
	for _, host := range []string{"example.com", "www.gov.uk", "bankofengland.co.uk", "Example.COM."} {
		if err := checkHostFloor(host); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
}

func TestADisguisedIPv4LiteralIsRefusedAsAnAddress(t *testing.T) {
	for _, h := range []string{"2130706433", "0x7f000001", "0177.0.0.1", "127.1", "0x7f.1", "8.8.8.8."} {
		err := checkHostFloor(h)
		if err == nil {
			t.Errorf("checkHostFloor(%q) = nil, want a 403 naming an IP address", h)
			continue
		}
		if code, msg := codeOf(t, err); code != "403" || !strings.Contains(msg, "IP address") {
			t.Errorf("checkHostFloor(%q) = %v, want a 403 naming an IP address", h, err)
		}
	}
	for _, h := range []string{"example.com", "a1.example", "0x.example", "v6.example."} {
		if err := checkHostFloor(h); err != nil {
			t.Errorf("checkHostFloor(%q) = %v, want nil", h, err)
		}
	}
}
