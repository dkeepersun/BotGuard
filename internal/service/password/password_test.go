package password

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := hashWith("Correct-Horse-9", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "pbkdf2-sha256$1000$") || !Verify(h, "Correct-Horse-9") || Verify(h, "correct-horse-9") {
		t.Fatal(h)
	}
	h2, _ := hashWith("Correct-Horse-9", 1000)
	if h == h2 {
		t.Fatal("salt not random")
	}
	for _, bad := range []string{"", "x", "pbkdf2-sha256$1$AA$AA", "md5$1000$AA$AA", h + "x"} {
		if Verify(bad, "Correct-Horse-9") {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestCheckPolicy(t *testing.T) {
	for pw, ok := range map[string]bool{
		"Short-1":                         false,
		"alllowercase123":                 false, // two classes
		"Lower-and-123":                   true,
		"ADMIN-pass-2026":                 false, // contains the username
		"Ab1-" + strings.Repeat("x", 130): false,
	} {
		err := Check(pw, "admin")
		if (err == nil) != ok || (err != nil && !errors.Is(err, ErrWeak)) {
			t.Errorf("%q: %v", pw, err)
		}
	}
	for i := 0; i < 20; i++ {
		pw, err := Generate()
		if err != nil || Check(pw, "admin") != nil {
			t.Fatalf("%q %v", pw, err)
		}
	}
}
