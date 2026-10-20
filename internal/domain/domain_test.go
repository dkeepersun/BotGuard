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
	} {
		if got, ok := NormalizeOrigin(in); !ok || got != want {
			t.Errorf("%s => %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "null", "ftp://example.com", "https://example.com/path",
		"https://u:p@example.com", "https://example.com?x=1", "example.com", "https://"} {
		if got, ok := NormalizeOrigin(in); ok {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}
