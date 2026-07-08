package chain

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedProxies matches the peer IPs allowed to set X-Forwarded-* headers.
type TrustedProxies struct {
	prefixes []netip.Prefix
	all      bool
}

// ParseTrustedProxies parses CIDRs and bare IPs; the literal "*" trusts every
// peer. An empty list trusts no peer, so X-Forwarded-* is stripped from every
// request (secure default). Invalid entries return an error (fail-fast at boot).
func ParseTrustedProxies(entries []string) (*TrustedProxies, error) {
	tp := &TrustedProxies{}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		switch {
		case e == "":
			continue
		case e == "*":
			tp.all = true
		case strings.Contains(e, "/"):
			p, err := netip.ParsePrefix(e)
			if err != nil {
				return nil, fmt.Errorf("chain: invalid trustedProxies CIDR %q: %w", e, err)
			}
			tp.prefixes = append(tp.prefixes, p.Masked())
		default:
			a, err := netip.ParseAddr(e)
			if err != nil {
				return nil, fmt.Errorf("chain: invalid trustedProxies IP %q: %w", e, err)
			}
			tp.prefixes = append(tp.prefixes, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return tp, nil
}

// Trusts reports whether addr is a trusted proxy.
func (tp *TrustedProxies) Trusts(addr netip.Addr) bool {
	if tp.all {
		return true
	}
	addr = addr.Unmap() // normalize IPv4-in-IPv6
	for _, p := range tp.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

var forwardedHeaders = []string{
	"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host",
	"X-Forwarded-Port", "X-Real-Ip", "Forwarded",
}

// Forwarded strips inbound X-Forwarded-* / Forwarded headers when the immediate
// peer is not a trusted proxy, so downstream code never consumes spoofable
// forwarded values. A trusted peer's headers pass through unchanged.
func Forwarded(tp *TrustedProxies) Middleware {
	return Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !peerTrusted(tp, r) {
				for _, h := range forwardedHeaders {
					r.Header.Del(h)
				}
			}
			next.ServeHTTP(w, r)
		})
	})
}

func peerTrusted(tp *TrustedProxies, r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return tp.Trusts(addr)
}
