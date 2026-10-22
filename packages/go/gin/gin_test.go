package botguardgin

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	botguard "github.com/dkeepersun/BotGuard/packages/go"
	"github.com/gin-gonic/gin"
)

// fakeGateway answers every verify with resp; signatures are covered by the
// core module's tests.
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

const passed = `{"code":"OK","data":{"success":true,"app_key":"app_test_1","action":"login","verified_at":1,"risk_level":"medium"}}`

func run(c *botguard.Client, opts ...Option) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	guard := botguard.NewGuard(c, "login", botguard.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	r.POST("/login", Protect(guard, opts...), func(c *gin.Context) {
		res, ok := Result(c)
		fromCtx, ok2 := botguard.ResultFrom(c.Request.Context())
		if !ok || !ok2 || res != fromCtx {
			c.String(500, "result missing")
			return
		}
		c.String(200, "ok "+res.RiskLevel)
	})
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("botguard_token=tok"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestProtect(t *testing.T) {
	if w := run(fakeGateway(t, 200, passed)); w.Code != 200 || w.Body.String() != "ok medium" {
		t.Fatalf("pass: %d %s", w.Code, w.Body)
	}
	w := run(fakeGateway(t, 200, `{"code":"OK","data":{"success":false,"reason":"TOKEN_EXPIRED"}}`))
	if w.Code != 403 || !strings.Contains(w.Body.String(), `"reason":"TOKEN_EXPIRED"`) {
		t.Fatalf("reject: %d %s", w.Code, w.Body)
	}
	w = run(fakeGateway(t, 503, `{"code":"UNAVAILABLE"}`))
	if w.Code != 503 || w.Header().Get("Retry-After") != "3" || !strings.Contains(w.Body.String(), "BOTGUARD_UNAVAILABLE") {
		t.Fatalf("unknown: %d %s", w.Code, w.Body)
	}
	w = run(fakeGateway(t, 401, `{"code":"UNAUTHORIZED"}`))
	if w.Code != 500 || !strings.Contains(w.Body.String(), "BOTGUARD_MISCONFIGURED") {
		t.Fatalf("caller: %d %s", w.Code, w.Body)
	}
}

func TestCustomResponses(t *testing.T) {
	w := run(fakeGateway(t, 200, `{"code":"OK","data":{"success":false,"reason":"TOKEN_EXPIRED"}}`),
		OnReject(func(c *gin.Context, res *botguard.Result) { c.AbortWithStatusJSON(400, gin.H{"err": res.Reason}) }))
	if w.Code != 400 || w.Body.String() != `{"err":"TOKEN_EXPIRED"}` {
		t.Fatalf("reject: %d %s", w.Code, w.Body)
	}
	w = run(fakeGateway(t, 503, `{}`), OnError(func(c *gin.Context, err error) {
		c.AbortWithStatus(map[bool]int{true: 502, false: 500}[botguard.IsUnknown(err)])
	}))
	if w.Code != 502 {
		t.Fatalf("error: %d", w.Code)
	}
}

// gin code usually answers with c.JSON, which does not abort; the protected
// handler must still not run.
func TestCustomResponsesWithoutAbort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := map[string]struct {
		status int
		resp   string
		opt    Option
		want   int
	}{
		"reject writes":  {200, `{"code":"OK","data":{"success":false,"reason":"TOKEN_INVALID"}}`, OnReject(func(c *gin.Context, _ *botguard.Result) { c.JSON(400, gin.H{"err": "captcha"}) }), 400},
		"reject silent":  {200, `{"code":"OK","data":{"success":false,"reason":"TOKEN_INVALID"}}`, OnReject(func(*gin.Context, *botguard.Result) {}), 403},
		"error writes":   {503, `{}`, OnError(func(c *gin.Context, _ error) { c.String(502, "later") }), 502},
		"error silent":   {401, `{"code":"UNAUTHORIZED"}`, OnError(func(*gin.Context, error) {}), 500},
		"unknown silent": {503, `{}`, OnError(func(*gin.Context, error) {}), 503},
	}
	for name, tc := range cases {
		ran := false
		r := gin.New()
		guard := botguard.NewGuard(fakeGateway(t, tc.status, tc.resp), "login", botguard.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
		r.POST("/login", Protect(guard, tc.opt), func(c *gin.Context) { ran = true; c.String(200, "side effect") })
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("botguard_token=tok"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if ran || w.Code != tc.want {
			t.Errorf("%s: handler ran=%v, status %d (want %d)", name, ran, w.Code, tc.want)
		}
	}
}

// The guard's step-up default reaches gin: a high-risk pass is held back
// unless the guard allows it.
func TestProtectStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	high := strings.Replace(passed, `"medium"`, `"high"`, 1)
	for _, allow := range []bool{false, true} {
		var opts []botguard.GuardOption
		if allow {
			opts = append(opts, botguard.AllowHighRisk())
		}
		ran := false
		r := gin.New()
		r.POST("/login", Protect(botguard.NewGuard(fakeGateway(t, 200, high), "login", opts...)), func(c *gin.Context) { ran = true })
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("botguard_token=tok"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if allow != ran || (!allow && (w.Code != 403 || !strings.Contains(w.Body.String(), "BOTGUARD_STEP_UP"))) {
			t.Errorf("allow=%v: ran=%v %d %s", allow, ran, w.Code, w.Body)
		}
	}
}
