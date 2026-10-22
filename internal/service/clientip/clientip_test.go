package clientip

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestResolve(t *testing.T) {
	r, err := New([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ remote, xff, want string }{
		"untrusted peer ignores header": {"203.0.113.9:1", "1.1.1.1", "203.0.113.9"},
		"trusted proxy":                 {"10.0.0.2:1", "198.51.100.7", "198.51.100.7"},
		"spoofed left entries":          {"10.0.0.2:1", "1.1.1.1, 198.51.100.7, 10.0.0.3", "198.51.100.7"},
		"malformed hop stops walk":      {"10.0.0.2:1", "198.51.100.7, junk", "10.0.0.2"},
		"no header":                     {"10.0.0.2:1", "", "10.0.0.2"},
	} {
		req := httptest.NewRequest("POST", "/", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := r.Resolve(req); got != netip.MustParseAddr(c.want) {
			t.Errorf("%s: %s", name, got)
		}
	}
	if _, err := New([]string{"nope"}); err == nil {
		t.Fatal("bad CIDR accepted")
	}
}

func TestBucket(t *testing.T) {
	if got := Bucket(netip.MustParseAddr("2001:db8:1:2:3:4:5:6")); got != "2001:db8:1:2::/64" {
		t.Fatal(got)
	}
	if got := Bucket(netip.MustParseAddr("192.0.2.1")); got != "192.0.2.1" {
		t.Fatal(got)
	}
}

func TestPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"2001:db8:1:2:3:4:5:6": "2001:db8:1::/48",
		"2001:db8:1:ffff::1":   "2001:db8:1::/48",
		"192.0.2.77":           "192.0.2.0/24",
		"::ffff:192.0.2.77":    "192.0.2.0/24",
	} {
		if got := Prefix(netip.MustParseAddr(in)); got != want {
			t.Errorf("Prefix(%s) = %s, want %s", in, got, want)
		}
	}
	if Prefix(netip.Addr{}) != "unknown" {
		t.Error("invalid address")
	}
}
