package web

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
)

type mapResolver map[string][]netip.Addr

func (m mapResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	addrs, ok := m[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	return addrs, nil
}

// recordingDial never connects; it records what it was asked to connect to.
func recordingDial(asked *[]string) DialFunc {
	return func(_ context.Context, _ string, address string) (net.Conn, error) {
		*asked = append(*asked, address)
		return nil, errors.New("recording dial does not connect")
	}
}

func TestTheDialConnectsToTheVettedAddressNotTheName(t *testing.T) {
	var asked []string
	d := guardedDial(mapResolver{"example.com": {netip.MustParseAddr("93.184.216.34")}}, recordingDial(&asked))
	_, err := d(context.Background(), "tcp", "example.com:443")
	if err == nil || !strings.Contains(err.Error(), "recording dial") {
		t.Fatalf("err = %v", err)
	}
	if len(asked) != 1 || asked[0] != "93.184.216.34:443" {
		t.Errorf("dialled %v, want the resolved address with the original port", asked)
	}
}

func TestAPrivateResolutionIsNeverDialled(t *testing.T) {
	for name, addrs := range map[string][]netip.Addr{
		"internal.example.com": {netip.MustParseAddr("10.1.2.3")},
		"mixed.example.com":    {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.1.2.3")},
		"mapped.example.com":   {netip.MustParseAddr("::ffff:127.0.0.1")},
		"meta.example.com":     {netip.MustParseAddr("169.254.169.254")},
	} {
		var asked []string
		d := guardedDial(mapResolver{name: addrs}, recordingDial(&asked))
		_, err := d(context.Background(), "tcp", name+":443")
		code, msg := codeOf(t, err)
		if code != "403" || !strings.Contains(msg, "resolved to a non-public address") {
			t.Errorf("%s: %s %q", name, code, msg)
		}
		if len(asked) != 0 {
			t.Errorf("%s: dialled %v", name, asked)
		}
	}
}

func TestANameThatDoesNotResolveIsNeverDialled(t *testing.T) {
	var asked []string
	d := guardedDial(mapResolver{}, recordingDial(&asked))
	_, err := d(context.Background(), "tcp", "nx.example.com:443")
	if code, _ := codeOf(t, err); code != "502" {
		t.Errorf("code %s, want 502", code)
	}
	if len(asked) != 0 {
		t.Errorf("dialled %v", asked)
	}
}

func TestTheFloorRunsAgainAtTheDial(t *testing.T) {
	// A redirect to a metadata name reaches the dialer even if something
	// upstream forgot to check it; the dialer refuses on its own.
	var asked []string
	d := guardedDial(mapResolver{"metadata.google.internal": {netip.MustParseAddr("93.184.216.34")}}, recordingDial(&asked))
	_, err := d(context.Background(), "tcp", "metadata.google.internal:443")
	if _, msg := codeOf(t, err); !strings.Contains(msg, "local or metadata name") {
		t.Errorf("%q", msg)
	}
	if len(asked) != 0 {
		t.Errorf("dialled %v", asked)
	}
}
