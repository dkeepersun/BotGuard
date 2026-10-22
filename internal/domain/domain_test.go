package domain

import "testing"

func TestNormalizeOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://Example.COM":       "https://example.com",
		"https://example.com:443":   "https://example.com",
		"http://example.com:80/":    "http://example.com",
		"http://localhost:5173":     "http://localhost:5173",
		"https://[::1]:8443":        "https://[::1]:8443",
		"https://a.example.com:444": "https://a.example.com:444",
		"app://com.example.shop":    "app://com.example.shop",
		"APP://Com.Example.Shop/":   "app://com.example.shop",
		"app://cn.example.my_app":   "app://cn.example.my_app",
	} {
		if got, ok := NormalizeOrigin(in); !ok || got != want {
			t.Errorf("%s => %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "null", "ftp://example.com", "https://example.com/path",
		"https://u:p@example.com", "https://example.com?x=1", "example.com", "https://",
		"app://", "app://com.example/path", "app://com.example:80", "app://.com.example", "app://com..example", "app://u@com.example"} {
		if got, ok := NormalizeOrigin(in); ok {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

func TestNormalizeOriginPattern(t *testing.T) {
	for in, want := range map[string]string{
		"*":                          "*",
		"https://*.Example.COM":      "https://*.example.com",
		"https://*.example.com:443":  "https://*.example.com",
		"http://*.example.com:8080/": "http://*.example.com:8080",
		"https://*.shop.example.com": "https://*.shop.example.com",
		"https://Shop.Example.com":   "https://shop.example.com",
		"app://com.example.shop":     "app://com.example.shop",
	} {
		if got, ok := NormalizeOriginPattern(in); !ok || got != want {
			t.Errorf("%s => %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "**", "*.example.com", "https://*", "https://*.com", "https://*.*.example.com",
		"https://a.*.example.com", "https://*example.com", "https://*.example.com/path", "ftp://*.example.com",
		"app://*.example", "https://*.[::1]", "https://*..example.com", "https://x*.example.com"} {
		if got, ok := NormalizeOriginPattern(in); ok {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

func TestOriginMatches(t *testing.T) {
	cases := []struct {
		pattern, origin string
		want            bool
	}{
		{"*", "https://anything.test", true},
		{"*", "app://com.example.shop", true},
		{"https://shop.example.com", "https://shop.example.com", true},
		{"https://shop.example.com", "https://shop.example.com:8443", false},
		{"https://*.example.com", "https://a.example.com", true},
		{"https://*.example.com", "https://a.b.example.com", true},
		{"https://*.example.com", "https://example.com", false},
		{"https://*.example.com", "http://a.example.com", false},
		{"https://*.example.com", "https://a.example.com:8443", false},
		{"https://*.example.com", "https://aexample.com", false},
		{"https://*.example.com", "https://a.example.com.evil.test", false},
		{"https://*.example.com:8443", "https://a.example.com:8443", true},
		{"https://*.example.com:8443", "https://a.example.com", false},
	}
	for _, c := range cases {
		if got := OriginMatches(c.pattern, c.origin); got != c.want {
			t.Errorf("%s ~ %s = %v, want %v", c.pattern, c.origin, got, c.want)
		}
	}
}
