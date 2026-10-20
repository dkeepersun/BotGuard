package signature

import (
	"encoding/json"
	"flag"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite shared fixtures")

const fixturePath = "../../../api/fixtures/hmac-signature-v1.json"

type fixture struct {
	Name      string `json:"name"`
	SecretKey string `json:"secret_key"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Timestamp int64  `json:"timestamp"`
	Nonce     string `json:"nonce"`
	Body      string `json:"body"`
	Canonical string `json:"canonical"`
	Signature string `json:"signature"`
}

func build() []fixture {
	cases := []fixture{
		{Name: "verify request", SecretKey: "kD3n9QxV2bLr7tYpZ0aFhW8sJmC4eUiN6oGq1RvXyTw", Method: "POST", Path: "/v1/verify",
			Timestamp: 1790000000, Nonce: "n0nce_ABCDEFGHIJKL",
			Body: `{"app_key":"app_demo","token":"bgt1.app_demo.x","action":"login","request_id":"r-1"}`},
		{Name: "empty body", SecretKey: "s3cret-key-with-utf8-中文", Method: "POST", Path: "/v1/verify",
			Timestamp: 0, Nonce: "0000000000000000", Body: ""},
	}
	for i := range cases {
		c := &cases[i]
		c.Canonical = Canonical(c.Method, c.Path, c.Timestamp, c.Nonce, []byte(c.Body))
		c.Signature = Sign([]byte(c.SecretKey), c.Canonical)
	}
	return cases
}

func TestSharedFixtures(t *testing.T) {
	if *update {
		b, _ := json.MarshalIndent(build(), "", "  ")
		if err := os.WriteFile(fixturePath, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var cases []fixture
	if err := json.Unmarshal(b, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("fixtures: %v", err)
	}
	for _, c := range cases {
		canon := Canonical(c.Method, c.Path, c.Timestamp, c.Nonce, []byte(c.Body))
		if canon != c.Canonical {
			t.Errorf("%s: canonical %q", c.Name, canon)
		}
		if !Equal([]byte(c.SecretKey), canon, c.Signature) {
			t.Errorf("%s: signature mismatch", c.Name)
		}
		if Equal([]byte(c.SecretKey+"x"), canon, c.Signature) {
			t.Errorf("%s: wrong key accepted", c.Name)
		}
	}
}

func TestFormats(t *testing.T) {
	if !ValidNonce("abcdefghijklmnop") || ValidNonce("short") || ValidNonce("has space in it!!") {
		t.Fatal("nonce pattern")
	}
	if ValidSignature("ABCDEF") || !ValidSignature(Sign([]byte("k"), "c")) {
		t.Fatal("signature pattern")
	}
}
