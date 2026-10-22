package botguard

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func form(token string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{"botguard_token": {token}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func serve(g *Guard, r *http.Request) (*httptest.ResponseRecorder, *Result) {
	var seen *Result
	h := g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = ResultFrom(r.Context())
		_, _ = w.Write([]byte("handled"))
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w, seen
}

func quietLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func TestGuardPass(t *testing.T) {
	gw := newGateway(t, func(_ int, b map[string]string) (int, http.Header, string) {
		if b["token"] != "good" || b["action"] != "login" {
			return 200, nil, `{"code":"OK","data":{"success":false,"reason":"TOKEN_INVALID"}}`
		}
		return 200, nil, pass(b)
	})
	g := NewGuard(newClient(t, gw), "login")
	w, res := serve(g, form("good"))
	if w.Code != 200 || w.Body.String() != "handled" || res == nil || !res.Passed {
		t.Fatalf("%d %s %+v", w.Code, w.Body, res)
	}
	// The header works for fetch submissions.
	r := httptest.NewRequest(http.MethodPost, "/login", nil)
	r.Header.Set(TokenHeader, "good")
	if w, _ := serve(g, r); w.Code != 200 {
		t.Fatalf("header token: %d", w.Code)
	}
}

func TestGuardReject(t *testing.T) {
	gw := newGateway(t, fixed(200, `{"code":"OK","data":{"success":false,"reason":"TOKEN_EXPIRED"}}`))
	g := NewGuard(newClient(t, gw), "login")
	w, res := serve(g, form("old"))
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 403 || res != nil || body["code"] != "BOTGUARD_REJECTED" || body["reason"] != "TOKEN_EXPIRED" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// A token in the URL query is not read: it would end up in access logs.
	r := httptest.NewRequest(http.MethodPost, "/login?botguard_token=good", nil)
	w, _ = serve(g, r)
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 403 || body["reason"] != ReasonTokenInvalid || gw.count() != 1 {
		t.Fatalf("query token: %d %s (%d calls)", w.Code, w.Body, gw.count())
	}
}

func TestGuardErrors(t *testing.T) {
	gw := newGateway(t, func(int, map[string]string) (int, http.Header, string) {
		return 503, http.Header{"Retry-After": {"5"}}, `{"code":"UNAVAILABLE"}`
	})
	log, buf := quietLogger()
	g := NewGuard(newClient(t, gw), "login", WithLogger(log))
	w, _ := serve(g, form("tok"))
	if w.Code != 503 || w.Header().Get("Retry-After") != "5" || !strings.Contains(w.Body.String(), "BOTGUARD_UNAVAILABLE") ||
		!strings.Contains(buf.String(), "核验结果未知") {
		t.Fatalf("unknown: %d %s %s", w.Code, w.Body, buf)
	}

	gw = newGateway(t, fixed(401, `{"code":"UNAUTHORIZED"}`))
	log, buf = quietLogger()
	g = NewGuard(newClient(t, gw), "login", WithLogger(log))
	w, _ = serve(g, form("tok"))
	if w.Code != 500 || !strings.Contains(w.Body.String(), "BOTGUARD_MISCONFIGURED") || !strings.Contains(buf.String(), "UNAUTHORIZED") {
		t.Fatalf("caller: %d %s %s", w.Code, w.Body, buf)
	}
}

func TestGuardOptions(t *testing.T) {
	var gotRID string
	gw := newGateway(t, func(_ int, b map[string]string) (int, http.Header, string) {
		gotRID = b["request_id"]
		return 200, nil, pass(b)
	})
	c := newClient(t, gw)
	rejected := false
	g := NewGuard(c, "signup",
		WithTokenFrom(func(r *http.Request) string { b, _ := io.ReadAll(r.Body); return string(b) }),
		WithRequestID(func(r *http.Request) string { return r.Header.Get("Idempotency-Key") }),
		WithGuardAppKey("app_other_1"),
		OnReject(func(w http.ResponseWriter, r *http.Request, res *Result) { rejected = true; w.WriteHeader(418) }))
	r := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader("tok"))
	r.Header.Set("Idempotency-Key", "order-42")
	w, res := serve(g, r)
	if w.Code != 200 || res.AppKey != "app_other_1" || gotRID != "order-42" || rejected {
		t.Fatalf("%d %+v %s", w.Code, res, gotRID)
	}
	// HandlerFunc has go-zero's rest.Middleware shape.
	var mw func(http.HandlerFunc) http.HandlerFunc = g.HandlerFunc
	_ = mw

	field := NewGuard(c, "login", WithTokenField("captcha"))
	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("captcha=tok"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w, _ := serve(field, r); w.Code != 200 {
		t.Fatalf("field: %d", w.Code)
	}
}

func TestNewGuardPanics(t *testing.T) {
	noApp, _ := New("https://x.example", "id", "k")
	withApp, _ := New("https://x.example", "id", "k", WithAppKey(testApp))
	for name, f := range map[string]func(){
		"nil client":  func() { NewGuard(nil, "login") },
		"no app key":  func() { NewGuard(noApp, "login") },
		"bad action":  func() { NewGuard(withApp, "Login") },
		"bad app key": func() { NewGuard(withApp, "login", WithGuardAppKey("x")) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
}

// Passes earned cheaply (fallback, capacity-degraded) or at high risk need a
// step-up by default; each can be allowed on its own.
func TestGuardStepUp(t *testing.T) {
	reply := func(extra string) func(int, map[string]string) (int, http.Header, string) {
		return func(_ int, b map[string]string) (int, http.Header, string) {
			return 200, nil, `{"code":"OK","data":{"success":true,"app_key":"` + b["app_key"] + `","action":"login","verified_at":1` + extra + `}}`
		}
	}
	cases := []struct {
		name, extra, reason string
		allow               GuardOption
	}{
		{"fallback", `,"challenge_kind":"fallback","risk_level":"low","risk_reasons":["FALLBACK_CHOSEN"]`, ReasonStepUpFallback, AllowFallback()},
		{"degraded", `,"challenge_kind":"invisible","risk_level":"low","risk_reasons":["CAPACITY_DEGRADED"]`, ReasonStepUpDegraded, AllowDegraded()},
		{"high risk", `,"challenge_kind":"spatial","risk_level":"high","risk_reasons":["WEBDRIVER"]`, ReasonStepUpHighRisk, AllowHighRisk()},
	}
	for _, c := range cases {
		gw := newGateway(t, reply(c.extra))
		w, res := serve(NewGuard(newClient(t, gw), "login"), form("tok"))
		var body map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != 403 || res != nil || body["code"] != "BOTGUARD_STEP_UP" || body["reason"] != c.reason {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
		if w, res := serve(NewGuard(newClient(t, gw), "login", c.allow), form("tok")); w.Code != 200 || res == nil || !res.Passed {
			t.Errorf("%s allowed: %d %s", c.name, w.Code, w.Body)
		}
	}
	// The held result keeps the gateway's fields for OnReject.
	gw := newGateway(t, reply(cases[2].extra))
	res, err := NewGuard(newClient(t, gw), "login").Check(form("tok"))
	if err != nil || res.Passed || res.Reason != ReasonStepUpHighRisk || res.RiskLevel != "high" || res.ChallengeKind != "spatial" {
		t.Fatalf("held result: %+v %v", res, err)
	}
	// An ordinary pass needs nothing more; Client.Verify never applies the policy.
	gw = newGateway(t, reply(`,"challenge_kind":"slider","risk_level":"medium"`))
	if w, _ := serve(NewGuard(newClient(t, gw), "login"), form("tok")); w.Code != 200 {
		t.Fatalf("medium pass: %d", w.Code)
	}
	gw = newGateway(t, reply(cases[0].extra))
	if res, err := newClient(t, gw).Verify(context.Background(), req("tok")); err != nil || !res.Passed || res.StepUp() != ReasonStepUpFallback {
		t.Fatalf("client: %+v %v", res, err)
	}
}
