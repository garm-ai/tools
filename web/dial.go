package web

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// Resolver is what guardedDial asks. *net.Resolver satisfies it; tests
// substitute a map.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// DialFunc opens the TCP connection once the address has been vetted. The
// default is a net.Dialer; tests substitute one that connects to a local
// server whatever address it is given, and record what it was given.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// guardedDial is the transport's DialContext: resolve the host itself,
// refuse unless every address is public, then dial the vetted address
// rather than letting the transport resolve again. Resolving twice is how
// a rebinding attack passes a check with one answer and connects with
// another. Every redirect hop opens a connection through here, so the
// resolved address is re-checked on every hop without the redirect logic
// having to remember to.
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
			// resolve is not evidence of anything, and nothing is fetched.
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
