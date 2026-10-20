package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/challenge"
	"github.com/dkeepersun/BotGuard/internal/service/clientip"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/router"
)

type sites struct{}

func (sites) SiteByKey(_ context.Context, k string) (*domain.Site, error) {
	if k != "site_public" {
		return nil, domain.ErrNotFound
	}
	return &domain.Site{ID: 1, PublicKey: k, Enabled: true, Origins: []string{"https://a.example"},
		Actions: map[string]domain.ActionPolicy{"login": {Enabled: true, Puzzles: 2, Difficulty: 8}}}, nil
}

func (sites) APIKey(_ context.Context, id string) (*domain.APIKey, error) {
	if id != "key_public" {
		return nil, domain.ErrNotFound
	}
	sum := sha256.Sum256([]byte("s3cret"))
	return &domain.APIKey{KeyID: id, SiteID: 1, SecretHash: sum[:], Enabled: true}, nil
}

type envelope struct {
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
	RequestID string          `json:"request_id"`
}

func newServer(t *testing.T, verifyQuota int) http.Handler {
	mr := miniredis.RunT(t)
	rds := redis.New(mr.Addr(), redis.WithIdentity())
	keys, _ := challenge.NewKeyring([]challenge.Key{{ID: "k1", Secret: bytes.Repeat([]byte("s"), 32), Active: true}})
	ips, _ := clientip.New(nil)
	g := &Gateway{
		Logic: &logic.Gateway{Sites: sites{}, State: repository.NewState(rds), Limiter: repository.NewRateLimiter(rds), Keys: keys,
			Settings: logic.Settings{ChallengeTTL: 2 * time.Minute, TokenTTL: time.Minute, ResultTTL: 10 * time.Minute,
				ClockSkew: 5 * time.Second, MaxDifficulty: 1 << 20, ChallengePerIPPerMinute: 100, SolvePerIPPerMinute: 100,
				VerifyPerKeyPerMinute: verifyQuota},
			Now: time.Now},
		IPs:   ips,
		Ready: func(context.Context) bool { return true },
	}
	srv := rest.MustNewServer(rest.RestConf{Host: "127.0.0.1", Port: 0, Timeout: 3000})
	g.Register(srv)
	// rest.Server does not expose ServeHTTP; route the registered handlers directly.
	r := router.NewRouter()
	for _, route := range srv.Routes() {
		if err := r.Handle(route.Method, route.Path, route.Handler); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func call(t *testing.T, h http.Handler, method, path, contentType, origin, auth, body string) (*httptest.ResponseRecorder, envelope) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.1:5000"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var env envelope
	if method != http.MethodOptions {
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s %s: %d %q", method, path, w.Code, w.Body.String())
		}
	}
	return w, env
}

func TestBrowserToServerFlow(t *testing.T) {
	h := newServer(t, 100)
	const origin = "https://a.example"

	w, _ := call(t, h, http.MethodOptions, "/v1/challenges", "", origin, "", "")
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Max-Age") != "600" {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}

	w, env := call(t, h, http.MethodPost, "/v1/challenges", "text/plain;charset=UTF-8", origin, "", `{"site_key":"site_public","action":"login"}`)
	if w.Code != 200 || env.Code != "OK" || w.Header().Get("Access-Control-Allow-Origin") != origin || env.RequestID == "" {
		t.Fatalf("challenge: %d %+v %v", w.Code, env, w.Header())
	}
	var ch logic.IssueOutput
	_ = json.Unmarshal(env.Data, &ch)
	nonces, _ := pow.Solve(ch.Algorithm, ch.ChallengeID, ch.Params)
	solveBody, _ := json.Marshal(map[string]any{"site_key": "site_public", "challenge": ch.Challenge, "nonces": nonces})

	w, env = call(t, h, http.MethodPost, "/v1/solutions", "application/json", origin, "", string(solveBody))
	if w.Code != 200 {
		t.Fatalf("solve: %d %+v", w.Code, env)
	}
	var sol logic.SolveOutput
	_ = json.Unmarshal(env.Data, &sol)

	w, env = call(t, h, http.MethodPost, "/v1/solutions", "application/json", origin, "", string(solveBody))
	if w.Code != http.StatusConflict || env.Code != "CHALLENGE_USED" {
		t.Fatalf("resolve: %d %+v", w.Code, env)
	}

	verify := func(token, rid string) (*httptest.ResponseRecorder, envelope) {
		b, _ := json.Marshal(map[string]string{"site_key": "site_public", "token": token, "action": "login", "request_id": rid})
		return call(t, h, http.MethodPost, "/v1/verify", "application/json", "", "Bearer key_public.s3cret", string(b))
	}
	w, env = verify(sol.Token, "r1")
	if w.Code != 200 || !strings.Contains(string(env.Data), `"success":true`) || env.RequestID != "r1" {
		t.Fatalf("verify: %d %+v", w.Code, env)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("verify must not send CORS headers")
	}
	w, env = verify(sol.Token, "r2")
	if w.Code != 200 || string(env.Data) != `{"success":false,"reason":"TOKEN_CONSUMED"}` {
		t.Fatalf("consumed: %d %s", w.Code, env.Data)
	}
}

func TestErrorEnvelopes(t *testing.T) {
	h := newServer(t, 1)
	w, env := call(t, h, http.MethodPost, "/v1/challenges", "text/plain", "https://evil.example", "", `{"site_key":"site_public","action":"login"}`)
	if w.Code != 403 || env.Code != "ORIGIN_NOT_ALLOWED" || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin: %d %+v %v", w.Code, env, w.Header())
	}
	w, env = call(t, h, http.MethodPost, "/v1/challenges", "application/x-www-form-urlencoded", "https://a.example", "", `site_key=x`)
	if w.Code != 400 || env.Code != "INVALID_REQUEST" {
		t.Fatalf("content type: %d %+v", w.Code, env)
	}
	w, env = call(t, h, http.MethodPost, "/v1/challenges", "application/json", "https://a.example", "", `{"site_key":"site_public"}{}`)
	if w.Code != 400 {
		t.Fatalf("trailing data: %d %+v", w.Code, env)
	}

	body := `{"site_key":"site_public","token":"x","action":"login","request_id":"r1"}`
	w, env = call(t, h, http.MethodPost, "/v1/verify", "application/json", "", "Bearer key_public.wrong", body)
	if w.Code != 401 || env.Code != "UNAUTHORIZED" || env.RequestID != "r1" {
		t.Fatalf("unauthorized: %d %+v", w.Code, env)
	}
	w, env = call(t, h, http.MethodPost, "/v1/verify", "application/json", "", "Bearer key_public.s3cret", body)
	if w.Code != 200 || string(env.Data) != `{"success":false,"reason":"TOKEN_INVALID"}` {
		t.Fatalf("invalid token: %d %s", w.Code, env.Data)
	}
	w, env = call(t, h, http.MethodPost, "/v1/verify", "application/json", "", "Bearer key_public.s3cret", body)
	if w.Code != 429 || env.Code != "RATE_LIMITED" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limited: %d %+v %v", w.Code, env, w.Header())
	}
}
