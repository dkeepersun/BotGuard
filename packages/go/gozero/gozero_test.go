package botguardgozero

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	botguard "github.com/dkeepersun/BotGuard/packages/go"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func fakeGateway(t *testing.T, status int, resp string) *botguard.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	c, err := botguard.New(srv.URL, "key_test_1", "secret", botguard.WithAppKey("app_test_1"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const passed = `{"code":"OK","data":{"success":true,"app_key":"app_test_1","action":"login","verified_at":1,"challenge_kind":"rotate"}}`

func run(c *botguard.Client, opts ...Option) *httptest.ResponseRecorder {
	guard := botguard.NewGuard(c, "login", botguard.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	h := Middleware(guard, opts...)(func(w http.ResponseWriter, r *http.Request) {
		res, ok := botguard.ResultFrom(r.Context())
		if !ok {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte("ok " + res.ChallengeKind))
	})
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("botguard_token=tok"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func TestMiddleware(t *testing.T) {
	if w := run(fakeGateway(t, 200, passed)); w.Code != 200 || w.Body.String() != "ok rotate" {
		t.Fatalf("pass: %d %s", w.Code, w.Body)
	}
	w := run(fakeGateway(t, 200, `{"code":"OK","data":{"success":false,"reason":"TOKEN_CONSUMED"}}`))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "TOKEN_CONSUMED") {
		t.Fatalf("reject: %d %s", w.Code, w.Body)
	}
	w = run(fakeGateway(t, 503, `{}`))
	if w.Code != 503 || w.Header().Get("Retry-After") != "3" || !strings.Contains(w.Body.String(), "BOTGUARD_UNAVAILABLE") {
		t.Fatalf("unknown: %d %s", w.Code, w.Body)
	}
}

func TestUseErrorHandler(t *testing.T) {
	httpx.SetErrorHandlerCtx(func(_ context.Context, err error) (int, any) {
		var rejected *RejectedError
		switch {
		case errors.As(err, &rejected):
			return 400, map[string]string{"reason": rejected.Result.Reason}
		case botguard.IsCallerError(err):
			return 501, nil
		default:
			return 502, nil
		}
	})
	defer httpx.SetErrorHandlerCtx(nil)
	if w := run(fakeGateway(t, 200, `{"code":"OK","data":{"success":false,"reason":"TOKEN_EXPIRED"}}`), UseErrorHandler()); w.Code != 400 || !strings.Contains(w.Body.String(), "TOKEN_EXPIRED") {
		t.Fatalf("reject: %d %s", w.Code, w.Body)
	}
	if w := run(fakeGateway(t, 401, `{"code":"UNAUTHORIZED"}`), UseErrorHandler()); w.Code != 501 {
		t.Fatalf("caller: %d", w.Code)
	}
	if w := run(fakeGateway(t, 503, `{}`), UseErrorHandler()); w.Code != 502 {
		t.Fatalf("unknown: %d", w.Code)
	}
}
