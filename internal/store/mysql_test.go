package store

import (
	"strings"
	"testing"
)

func TestNormalizeMySQLDSN(t *testing.T) {
	got, err := NormalizeMySQLDSN("u:p@tcp(127.0.0.1:3306)/bg?multiStatements=true&loc=Local")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"parseTime=true", "time_zone=%27%2B00%3A00%27"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s missing %s", got, want)
		}
	}
	for _, bad := range []string{"multiStatements", "loc=Local"} {
		if strings.Contains(got, bad) {
			t.Errorf("%s still has %s", got, bad)
		}
	}
	if _, err := NormalizeMySQLDSN(""); err == nil {
		t.Fatal("empty DSN accepted")
	}
	if _, err := NormalizeMySQLDSN("u:p@tcp(127.0.0.1:3306)/"); err == nil {
		t.Fatal("DSN without a database accepted")
	}
}
