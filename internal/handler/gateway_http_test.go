package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	"github.com/dkeepersun/BotGuard/internal/service/signature"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/router"
)

type apps struct{}

func (apps) AppByKey(_ context.Context, k string) (*domain.App, error) {
	if k != "app_public" {
		return nil, domain.ErrNotFound
	}
	return &domain.App{ID: 1, AppKey: k, Enabled: true, Origins: []string{"https://a.example"},
		Actions: map[string]domain.ActionPolicy{"login": {Enabled: true, Mode: domain.ModeInvisible, Puzzles: 2, Difficulty: 8}}}, nil
}

func (apps) SecretPair(_ context.Context, id string) (*domain.SecretPair, error) {
	if id != "key_public" {
		return nil, domain.ErrNotFound
	}
	return &domain.SecretPair{SecretID: id, AppID: 1, SecretKey: []byte("s3cret"), Enabled: true}, nil
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
		Logic: &logic.Gateway{Apps: apps{}, State: repository.NewState(rds), Limiter: repository.NewRateLimiter(rds), Keys: keys,
			Settings: logic.Settings{ChallengeTTL: 2 * time.Minute, TokenTTL: time.Minute, ResultTTL: 10 * time.Minute, SignatureWindow: 5 * time.Minute,
				ClockSkew: 5 * time.Second, MaxDifficulty: 1 << 20, ChallengePerIPPerMinute: 100, SolvePerIPPerMinute: 100,
				VerifyPerKeyPerMinute: verifyQuota, RiskMedium: 30, RiskHigh: 70, HighDifficultyFactor: 4, FallbackDifficultyFactor: 8,
				FallbackPerIPPerMinute: 5, FailureWindow: 10 * time.Minute, SliderTolerance: 4},
			Now: time.Now},
		IPs:   ips,
		Ready: func(context.Context) string { return "" },
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
	// auth is the secret key to sign with, for secret id "key_public".
	if auth != "" {
		ts := time.Now().Unix()
		nonce := fmt.Sprintf("httptest_nonce_%d", time.Now().UnixNano())
		req.Header.Set(signature.HeaderSecretID, "key_public")
		req.Header.Set(signature.HeaderTimestamp, strconv.FormatInt(ts, 10))
		req.Header.Set(signature.HeaderNonce, nonce)
		req.Header.Set(signature.HeaderSignature, signature.Sign([]byte(auth), signature.Canonical("POST", "/v1/verify", ts, nonce, []byte(body))))
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

	w, env := call(t, h, http.MethodPost, "/v1/challenges", "text/plain;charset=UTF-8", origin, "", `{"app_key":"app_public","action":"login"}`)
	if w.Code != 200 || env.Code != "OK" || w.Header().Get("Access-Control-Allow-Origin") != origin || env.RequestID == "" {
		t.Fatalf("challenge: %d %+v %v", w.Code, env, w.Header())
	}
	var ch logic.IssueOutput
	_ = json.Unmarshal(env.Data, &ch)
	nonces, _ := pow.Solve(ch.Algorithm, ch.ChallengeID, ch.Params)
	solveBody, _ := json.Marshal(map[string]any{"app_key": "app_public", "challenge": ch.Challenge, "nonces": nonces})

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
		b, _ := json.Marshal(map[string]string{"app_key": "app_public", "token": token, "action": "login", "request_id": rid})
		return call(t, h, http.MethodPost, "/v1/verify", "application/json", "", "s3cret", string(b))
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
	w, env := call(t, h, http.MethodPost, "/v1/challenges", "text/plain", "https://evil.example", "", `{"app_key":"app_public","action":"login"}`)
	if w.Code != 403 || env.Code != "ORIGIN_NOT_ALLOWED" || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin: %d %+v %v", w.Code, env, w.Header())
	}
	w, env = call(t, h, http.MethodPost, "/v1/challenges", "application/x-www-form-urlencoded", "https://a.example", "", `app_key=x`)
	if w.Code != 400 || env.Code != "INVALID_REQUEST" {
		t.Fatalf("content type: %d %+v", w.Code, env)
	}
	w, env = call(t, h, http.MethodPost, "/v1/challenges", "application/json", "https://a.example", "", `{"app_key":"app_public"}{}`)
	if w.Code != 400 {
		t.Fatalf("trailing data: %d %+v", w.Code, env)
	}
	// Oversized bodies get the contract's JSON 400, not a bare 413 (cmd/gateway
	// turns go-zero's MaxBytes middleware off for this).
	big := `{"app_key":"app_public","challenge":"` + strings.Repeat("x", 40<<10) + `"}`
	w, env = call(t, h, http.MethodPost, "/v1/solutions", "text/plain", "https://a.example", "", big)
	if w.Code != 400 || env.Code != "INVALID_REQUEST" {
		t.Fatalf("oversized: %d %+v", w.Code, env)
	}

	body := `{"app_key":"app_public","token":"x","action":"login","request_id":"r1"}`
	w, env = call(t, h, http.MethodPost, "/v1/verify", "application/json", "", "wrong", body)
	if w.Code != 401 || env.Code != "UNAUTHORIZED" || env.RequestID != "r1" {
		t.Fatalf("unauthorized: %d %+v", w.Code, env)
	}
	w, env = call(t, h, http.MethodPost, "/v1/verify", "application/json", "", "s3cret", body)
	if w.Code != 200 || string(env.Data) != `{"success":false,"reason":"TOKEN_INVALID"}` {
		t.Fatalf("invalid token: %d %s", w.Code, env.Data)
	}
	w, env = call(t, h, http.MethodPost, "/v1/verify", "application/json", "", "s3cret", body)
	if w.Code != 429 || env.Code != "RATE_LIMITED" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limited: %d %+v %v", w.Code, env, w.Header())
	}
}
