// Package clientip resolves the caller address, trusting X-Forwarded-For
// only when the hop that added it is an explicitly configured proxy.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type Resolver struct {
	trusted []netip.Prefix
}

func New(cidrs []string) (*Resolver, error) {
	r := &Resolver{}
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", c, err)
		}
		r.trusted = append(r.trusted, p.Masked())
	}
	return r, nil
}

func (r *Resolver) isTrusted(a netip.Addr) bool {
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Resolve walks X-Forwarded-For from the right and returns the first
// address not owned by a trusted proxy.
func (r *Resolver) Resolve(req *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	addr = addr.Unmap()
	if !r.isTrusted(addr) {
		return addr
	}
	hops := strings.Split(strings.Join(req.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			// A malformed hop means we cannot trust anything to its left.
			return addr
		}
		hop = hop.Unmap()
		if !r.isTrusted(hop) {
			return hop
		}
		addr = hop
	}
	return addr
}

// Prefix is a coarser group than Bucket: IPv4 /24 and IPv6 /48, roughly
// what one customer, data centre rack or tunnel broker allocation holds.
// Limits on it make many addresses from one network cost like one.
func Prefix(a netip.Addr) string {
	if !a.IsValid() {
		return "unknown"
	}
	a = a.Unmap()
	bits := 24
	if a.Is6() {
		bits = 48
	}
	p, _ := a.Prefix(bits)
	return p.String()
}

// Bucket groups IPv6 clients by /64 so rotating within one allocation does
// not reset rate limits.
func Bucket(a netip.Addr) string {
	if !a.IsValid() {
		return "unknown"
	}
	if a.Is6() {
		p, _ := a.Prefix(64)
		return p.String()
	}
	return a.String()
}
