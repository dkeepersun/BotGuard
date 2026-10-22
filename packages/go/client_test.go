package botguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testSecretID  = "key_test_1"
	testSecretKey = "s3cret-key"
	testApp       = "app_test_1"
)

func TestSignatureFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/hmac-signature-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
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
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("fixtures: %v", err)
	}
	for _, c := range cases {
		got := canonical(c.Method, c.Path, c.Timestamp, c.Nonce, []byte(c.Body))
		if got != c.Canonical {
			t.Errorf("%s: canonical %q, want %q", c.Name, got, c.Canonical)
		}
		if s := sign([]byte(c.SecretKey), got); s != c.Signature {
			t.Errorf("%s: signature %s, want %s", c.Name, s, c.Signature)
		}
	}
}

// The module carries its own copy of the shared fixture (a module zip holds
// nothing outside its directory); inside the monorepo it must not drift.
func TestFixtureMatchesContract(t *testing.T) {
	shared, err := os.ReadFile("../../api/fixtures/hmac-signature-v1.json")
	if err != nil {
		t.Skip("not inside the BotGuard repository")
	}
	local, _ := os.ReadFile("testdata/hmac-signature-v1.json")
	if !bytes.Equal(shared, local) {
		t.Fatal("testdata/hmac-signature-v1.json differs from api/fixtures; copy it again")
	}
}

// gateway is a fake /v1/verify that checks the signature like the real one
// and answers with reply.
type gateway struct {
	mu     sync.Mutex
	calls  []call
	reply  func(n int, body map[string]string) (status int, header http.Header, resp string)
	server *httptest.Server
}

type call struct {
	body   string
	nonce  string
	signed bool
}

func newGateway(t *testing.T, reply func(n int, body map[string]string) (int, http.Header, string)) *gateway {
	g := &gateway{reply: reply}
	g.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		ts := r.Header.Get(headerTimestamp)
		var tsInt int64
		_ = json.Unmarshal([]byte(ts), &tsInt)
		want := sign([]byte(testSecretKey), canonical("POST", "/v1/verify", tsInt, r.Header.Get(headerNonce), raw))
		ok := r.Method == http.MethodPost && r.URL.Path == "/v1/verify" && r.Header.Get(headerSecretID) == testSecretID &&
			r.Header.Get(headerSignature) == want && len(r.Header.Get(headerNonce)) >= 16
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		g.mu.Lock()
		g.calls = append(g.calls, call{body: string(raw), nonce: r.Header.Get(headerNonce), signed: ok})
		n := len(g.calls)
		g.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"UNAUTHORIZED","message":"签名错误"}`))
			return
		}
		status, header, resp := g.reply(n, body)
		for k, v := range header {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(g.server.Close)
	return g
}

func (g *gateway) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.calls)
}

func pass(body map[string]string) string {
	return `{"code":"OK","data":{"success":true,"app_key":"` + body["app_key"] + `","action":"` + body["action"] +
		`","verified_at":1790000030,"challenge_kind":"slider","risk_level":"medium","risk_reasons":["NO_SIGNALS",3],"origin":"https://m.shop.example"},"request_id":"x"}`
}

func fixed(status int, resp string) func(int, map[string]string) (int, http.Header, string) {
	return func(int, map[string]string) (int, http.Header, string) { return status, nil, resp }
}

func newClient(t *testing.T, g *gateway, opts ...Option) *Client {
	t.Helper()
	c, err := New(g.server.URL+"/", testSecretID, testSecretKey, append([]Option{WithAppKey(testApp)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func req(token string) VerifyRequest {
	return VerifyRequest{AppKey: testApp, Token: token, Action: "login", RequestID: "r-1"}
}

func TestPassed(t *testing.T) {
	g := newGateway(t, func(_ int, b map[string]string) (int, http.Header, string) { return 200, nil, pass(b) })
	res, err := newClient(t, g).Verify(context.Background(), req("bgt1.app.x"))
	if err != nil || !res.Passed || res.Rejected() {
		t.Fatalf("%+v %v", res, err)
	}
	if res.AppKey != testApp || res.Action != "login" || res.RequestID != "r-1" || res.VerifiedAt.Unix() != 1790000030 {
		t.Fatalf("%+v", res)
	}
	// Risk fields are informational; a non-string reason is dropped.
	if res.ChallengeKind != "slider" || res.RiskLevel != "medium" || len(res.RiskReasons) != 1 || res.RiskReasons[0] != "NO_SIGNALS" ||
		res.Origin != "https://m.shop.example" {
		t.Fatalf("risk: %+v", res)
	}
	if !g.calls[0].signed || g.calls[0].body != `{"action":"login","app_key":"app_test_1","request_id":"r-1","token":"bgt1.app.x"}` {
		t.Fatalf("request: %+v", g.calls[0])
	}
}

func TestOlderGatewayWithoutRiskFields(t *testing.T) {
	g := newGateway(t, func(_ int, b map[string]string) (int, http.Header, string) {
		return 200, nil, `{"code":"OK","data":{"success":true,"app_key":"` + b["app_key"] + `","action":"login","verified_at":1}}`
	})
	res, err := newClient(t, g).Verify(context.Background(), req("tok"))
	if err != nil || !res.Passed || res.ChallengeKind != "" || len(res.RiskReasons) != 0 || res.Origin != "" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRejected(t *testing.T) {
	for _, reason := range []string{ReasonTokenExpired, "SOME_FUTURE_REASON"} {
		g := newGateway(t, fixed(200, `{"code":"OK","data":{"success":false,"reason":"`+reason+`"}}`))
		res, err := newClient(t, g).Verify(context.Background(), req("tok"))
		if err != nil || res.Passed || res.Reason != reason {
			t.Fatalf("%s: %+v %v", reason, res, err)
		}
	}
}

func TestLocalChecks(t *testing.T) {
	g := newGateway(t, fixed(200, `{}`))
	c := newClient(t, g)
	for _, tok := range []string{"", "has space", "中文", strings.Repeat("a", 257)} {
		res, err := c.Verify(context.Background(), req(tok))
		if err != nil || res.Passed || res.Reason != ReasonTokenInvalid {
			t.Fatalf("token %q: %+v %v", tok, res, err)
		}
	}
	bad := []VerifyRequest{
		{AppKey: "short", Token: "t", Action: "login", RequestID: "r"},
		{AppKey: testApp, Token: "t", Action: "Login", RequestID: "r"},
		{AppKey: testApp, Token: "t", Action: "login", RequestID: "r 1"},
	}
	for _, r := range bad {
		_, err := c.Verify(context.Background(), r)
		var ce *CallerError
		if !errors.As(err, &ce) || ce.Status != 0 || ce.Code != "INVALID_REQUEST" {
			t.Fatalf("%+v: %v", r, err)
		}
	}
	if g.count() != 0 {
		t.Fatalf("local checks sent %d requests", g.count())
	}
}

func TestCallerErrors(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		code   string
	}{
		"unauthorized": {401, `{"code":"UNAUTHORIZED","message":"签名错误"}`, "UNAUTHORIZED"},
		"conflict":     {409, `{"code":"IDEMPOTENCY_CONFLICT","message":"x"}`, "IDEMPOTENCY_CONFLICT"},
		"not found":    {404, `<html>nginx</html>`, ""},
		"redirect":     {302, ``, ""},
	}
	for name, tc := range cases {
		g := newGateway(t, func(int, map[string]string) (int, http.Header, string) {
			h := http.Header{}
			if tc.status == 302 {
				h.Set("Location", "https://elsewhere.example/")
			}
			return tc.status, h, tc.body
		})
		_, err := newClient(t, g, WithRetries(3)).Verify(context.Background(), req("tok"))
		var ce *CallerError
		if !errors.As(err, &ce) || ce.Status != tc.status || ce.Code != tc.code || !IsCallerError(err) || IsUnknown(err) {
			t.Fatalf("%s: %v", name, err)
		}
		if g.count() != 1 {
			t.Fatalf("%s: caller errors must not be retried or redirected (%d calls)", name, g.count())
		}
	}
}

func TestUnknownAndRetries(t *testing.T) {
	// 503 twice, then a pass: retried with fresh signatures, same body.
	g := newGateway(t, func(n int, b map[string]string) (int, http.Header, string) {
		if n < 3 {
			return 503, nil, `{"code":"UNAVAILABLE","message":"x"}`
		}
		return 200, nil, pass(b)
	})
	res, err := newClient(t, g, WithRetries(2)).Verify(context.Background(), req("tok"))
	if err != nil || !res.Passed || g.count() != 3 {
		t.Fatalf("%+v %v (%d calls)", res, err, g.count())
	}
	if g.calls[0].nonce == g.calls[1].nonce || g.calls[0].body != g.calls[2].body {
		t.Fatal("each retry needs a new nonce and the same body")
	}

	// Without retries the first 503 is returned as unknown.
	g = newGateway(t, func(int, map[string]string) (int, http.Header, string) {
		return 503, http.Header{"Retry-After": {"7"}}, `{"code":"UNAVAILABLE","message":"x"}`
	})
	_, err = newClient(t, g).Verify(context.Background(), req("tok"))
	var ue *UnknownError
	if !errors.As(err, &ue) || ue.Status != 503 || ue.Code != "UNAVAILABLE" || ue.RetryAfter != 7*time.Second || ue.Protocol || !IsUnknown(err) {
		t.Fatalf("503: %v", err)
	}

	// 429 is never retried here; Retry-After as an HTTP-date.
	g = newGateway(t, func(int, map[string]string) (int, http.Header, string) {
		return 429, http.Header{"Retry-After": {time.Unix(1790000060, 0).UTC().Format(http.TimeFormat)}}, `{"code":"RATE_LIMITED"}`
	})
	c := newClient(t, g, WithRetries(3))
	c.now = func() time.Time { return time.Unix(1790000000, 0) }
	_, err = c.Verify(context.Background(), req("tok"))
	if !errors.As(err, &ue) || ue.Status != 429 || ue.RetryAfter != 60*time.Second || g.count() != 1 {
		t.Fatalf("429: %v (%d calls)", err, g.count())
	}
}

func TestProtocolErrors(t *testing.T) {
	cases := map[string]func(map[string]string) (int, string){
		"not json":    func(map[string]string) (int, string) { return 200, `ok` },
		"no success":  func(map[string]string) (int, string) { return 200, `{"code":"OK","data":{}}` },
		"no reason":   func(map[string]string) (int, string) { return 200, `{"code":"OK","data":{"success":false}}` },
		"not ok code": func(map[string]string) (int, string) { return 200, `{"code":"X","data":{"success":true}}` },
		"other action": func(b map[string]string) (int, string) {
			return 200, strings.Replace(pass(b), `"login"`, `"signup"`, 1)
		},
		"float time":     func(b map[string]string) (int, string) { return 200, strings.Replace(pass(b), "1790000030", "1.5", 1) },
		"oversized":      func(map[string]string) (int, string) { return 200, `{"x":"` + strings.Repeat("a", 70<<10) + `"}` },
		"unexpected 204": func(map[string]string) (int, string) { return 204, `` },
		"string success": func(map[string]string) (int, string) { return 200, `{"code":"OK","data":{"success":"true"}}` },
		"missing app_key": func(map[string]string) (int, string) {
			return 200, `{"code":"OK","data":{"success":true,"action":"login","verified_at":1}}`
		},
	}
	for name, reply := range cases {
		g := newGateway(t, func(_ int, b map[string]string) (int, http.Header, string) {
			s, r := reply(b)
			return s, nil, r
		})
		_, err := newClient(t, g, WithRetries(3)).Verify(context.Background(), req("tok"))
		var ue *UnknownError
		if !errors.As(err, &ue) || !ue.Protocol {
			t.Fatalf("%s: %v", name, err)
		}
		if g.count() != 1 {
			t.Fatalf("%s: protocol errors must not be retried", name)
		}
	}
}

func TestNetworkErrorAndTimeout(t *testing.T) {
	g := newGateway(t, fixed(200, `{}`))
	url := g.server.URL
	g.server.Close()
	c, _ := New(url, testSecretID, testSecretKey)
	_, err := c.Verify(context.Background(), req("tok"))
	var ue *UnknownError
	if !errors.As(err, &ue) || ue.Status != 0 || ue.Err == nil || ue.Protocol {
		t.Fatalf("network: %v", err)
	}

	done := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-done }))
	defer slow.Close()
	defer close(done)
	c, _ = New(slow.URL, testSecretID, testSecretKey, WithTimeout(time.Millisecond))
	start := time.Now()
	_, err = c.Verify(context.Background(), req("tok"))
	if !IsUnknown(err) || time.Since(start) > time.Second {
		t.Fatalf("timeout: %v after %s", err, time.Since(start))
	}
}

func TestOptionsAndSecrets(t *testing.T) {
	if _, err := New("https://x.example", "", "k"); err == nil {
		t.Fatal("empty secret_id accepted")
	}
	if _, err := New("x.example", "id", "k"); err == nil {
		t.Fatal("endpoint without scheme accepted")
	}
	c, _ := New("https://x.example", "id", "very-secret", WithTimeout(time.Nanosecond))
	if c.http.Timeout != MinTimeout {
		t.Fatalf("timeout floor: %s", c.http.Timeout)
	}
	// WithHTTPClient keeps a timeout whatever the order.
	c, _ = New("https://x.example", "id", "very-secret", WithTimeout(2*time.Second), WithHTTPClient(&http.Client{}))
	if c.http.Timeout != 2*time.Second || c.http.CheckRedirect == nil {
		t.Fatalf("http client: %+v", c.http)
	}
	c, _ = New("https://x.example", "id", "very-secret", WithHTTPClient(&http.Client{}))
	if c.http.Timeout != DefaultTimeout {
		t.Fatalf("default timeout: %s", c.http.Timeout)
	}
	for _, s := range []string{c.String(), c.GoString()} {
		if strings.Contains(s, "very-secret") || !strings.Contains(s, "[REDACTED]") {
			t.Fatalf("secret leaks: %s", s)
		}
	}
	if _, err := c.VerifyToken(context.Background(), "tok", "login"); !IsCallerError(err) {
		t.Fatalf("VerifyToken without app key: %v", err)
	}
	id := NewRequestID()
	if !requestIDPattern.MatchString(id) || len(id) != 36 || id[14] != '4' {
		t.Fatalf("request id %q", id)
	}
}

func TestVerifyToken(t *testing.T) {
	g := newGateway(t, func(_ int, b map[string]string) (int, http.Header, string) { return 200, nil, pass(b) })
	res, err := newClient(t, g).VerifyToken(context.Background(), "tok", "login")
	if err != nil || !res.Passed || len(res.RequestID) != 36 {
		t.Fatalf("%+v %v", res, err)
	}
}
