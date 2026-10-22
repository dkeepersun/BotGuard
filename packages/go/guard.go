package botguard

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

const (
	// DefaultTokenField is the form field the browser widget fills.
	DefaultTokenField = "botguard_token"
	// TokenHeader is read when the form field is empty, for pages that
	// submit with fetch and put the token in a header instead.
	TokenHeader = "X-BotGuard-Token"
)

// Guard verifies the token on incoming requests for one action. Use
// Middleware with net/http (and anything built on it, such as chi or echo's
// WrapMiddleware); the gin and go-zero adapters build on Check and the
// response handlers. Each request's token is verified once, so put the guard
// after cheap checks (field validation, auth) and before side effects.
type Guard struct {
	client    *Client
	appKey    string
	action    string
	tokenFrom func(*http.Request) string
	requestID func(*http.Request) string
	onReject  func(http.ResponseWriter, *http.Request, *Result)
	onError   func(http.ResponseWriter, *http.Request, error)
	logger    *slog.Logger
}

// GuardOption configures a Guard.
type GuardOption func(*Guard)

// WithTokenFrom replaces how the token is read, e.g. from a JSON body the
// handler has already decoded. The default reads the botguard_token form
// field (request body only, never the URL query), then the
// X-BotGuard-Token header.
func WithTokenFrom(f func(*http.Request) string) GuardOption {
	return func(g *Guard) { g.tokenFrom = f }
}

// WithTokenField reads the form field name instead of botguard_token.
func WithTokenField(name string) GuardOption {
	return func(g *Guard) { g.tokenFrom = func(r *http.Request) string { return formOrHeader(r, name) } }
}

// WithRequestID derives the request_id from the request, e.g. from an
// idempotency key the client sends, so a retried request gets the gateway's
// idempotent answer. The default is a fresh UUID per request.
func WithRequestID(f func(*http.Request) string) GuardOption {
	return func(g *Guard) { g.requestID = f }
}

// WithGuardAppKey verifies against this app instead of the client's WithAppKey.
func WithGuardAppKey(appKey string) GuardOption { return func(g *Guard) { g.appKey = appKey } }

// OnReject replaces the response for a rejected token (default: 403 JSON
// {"code":"BOTGUARD_REJECTED","reason":...,"message":...}).
func OnReject(f func(http.ResponseWriter, *http.Request, *Result)) GuardOption {
	return func(g *Guard) { g.onReject = f }
}

// OnError replaces the response for a *CallerError or *UnknownError
// (default: 500 BOTGUARD_MISCONFIGURED or 503 BOTGUARD_UNAVAILABLE JSON,
// after logging the error).
func OnError(f func(http.ResponseWriter, *http.Request, error)) GuardOption {
	return func(g *Guard) { g.onError = f }
}

// WithLogger logs caller errors and unknown results there instead of
// slog.Default(). A misconfiguration must not look like "the user failed".
func WithLogger(l *slog.Logger) GuardOption { return func(g *Guard) { g.logger = l } }

// NewGuard returns a guard for action. It panics when the app key or action
// is missing or malformed: that is a programming error found at startup,
// not a per-request failure.
func NewGuard(client *Client, action string, opts ...GuardOption) *Guard {
	if client == nil {
		panic("botguard: NewGuard 需要 Client")
	}
	g := &Guard{client: client, appKey: client.appKey, action: action,
		tokenFrom: func(r *http.Request) string { return formOrHeader(r, DefaultTokenField) },
		requestID: func(*http.Request) string { return NewRequestID() }}
	for _, o := range opts {
		o(g)
	}
	if !appKeyPattern.MatchString(g.appKey) {
		panic("botguard: NewGuard 需要合法的 app_key（WithAppKey 或 WithGuardAppKey）")
	}
	if !actionPattern.MatchString(action) {
		panic("botguard: action 格式错误：应为 [a-z0-9_]，1–32 个字符")
	}
	if g.onReject == nil {
		g.onReject = defaultReject
	}
	if g.onError == nil {
		g.onError = g.defaultError
	}
	return g
}

func formOrHeader(r *http.Request, field string) string {
	if v := r.PostFormValue(field); v != "" {
		return v
	}
	return r.Header.Get(TokenHeader)
}

// Check verifies the request's token. It returns a Result (passed or
// rejected) or a *CallerError / *UnknownError, exactly like Client.Verify.
func (g *Guard) Check(r *http.Request) (*Result, error) {
	return g.client.Verify(r.Context(), VerifyRequest{AppKey: g.appKey, Token: g.tokenFrom(r),
		Action: g.action, RequestID: g.requestID(r)})
}

// Middleware lets the request through only when its token passes; the
// handler then finds the result with ResultFrom(r.Context()).
func (g *Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res, err := g.Check(r)
		switch {
		case err != nil:
			g.Fail(w, r, err)
		case !res.Passed:
			g.Reject(w, r, res)
		default:
			next.ServeHTTP(w, r.WithContext(WithResult(r.Context(), res)))
		}
	})
}

// HandlerFunc is Middleware for http.HandlerFunc. Its type matches
// go-zero's rest.Middleware, so server.Use(guard.HandlerFunc) works without
// the adapter.
func (g *Guard) HandlerFunc(next http.HandlerFunc) http.HandlerFunc {
	return g.Middleware(next).ServeHTTP
}

// Reject writes the response for a rejected token.
func (g *Guard) Reject(w http.ResponseWriter, r *http.Request, res *Result) { g.onReject(w, r, res) }

// Fail writes the response for a caller error or unknown result.
func (g *Guard) Fail(w http.ResponseWriter, r *http.Request, err error) { g.onError(w, r, err) }

// Log records a caller error or unknown result; adapters with their own
// responses call it so a failure is never silent.
func (g *Guard) Log(r *http.Request, err error) {
	l := g.logger
	if l == nil {
		l = slog.Default()
	}
	var caller *CallerError
	if errors.As(err, &caller) {
		l.ErrorContext(r.Context(), "botguard: 核验配置错误，请检查密钥、时钟与应用配置", "action", g.action, "err", err)
		return
	}
	l.ErrorContext(r.Context(), "botguard: 核验结果未知，已拒绝本次请求", "action", g.action, "err", err)
}

// ErrorResponse is the default status and body for a caller error or
// unknown result; adapters reuse it so every framework answers alike.
func ErrorResponse(err error) (status int, body map[string]string, retryAfter time.Duration) {
	var unknown *UnknownError
	if errors.As(err, &unknown) {
		return http.StatusServiceUnavailable, map[string]string{"code": "BOTGUARD_UNAVAILABLE", "message": "人机验证服务暂时不可用，请稍后再试"}, unknown.RetryAfter
	}
	return http.StatusInternalServerError, map[string]string{"code": "BOTGUARD_MISCONFIGURED", "message": "人机验证配置错误"}, 0
}

// RejectResponse is the default status and body for a rejected token.
func RejectResponse(res *Result) (status int, body map[string]string) {
	return http.StatusForbidden, map[string]string{"code": "BOTGUARD_REJECTED", "reason": res.Reason, "message": "人机验证未通过，请重新验证"}
}

func defaultReject(w http.ResponseWriter, _ *http.Request, res *Result) {
	status, body := RejectResponse(res)
	writeJSON(w, status, body)
}

func (g *Guard) defaultError(w http.ResponseWriter, r *http.Request, err error) {
	g.Log(r, err)
	status, body, after := ErrorResponse(err)
	if after > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(after/time.Second)))
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type resultKey struct{}

// WithResult stores a passed result in ctx for the handler.
func WithResult(ctx context.Context, res *Result) context.Context {
	return context.WithValue(ctx, resultKey{}, res)
}

// ResultFrom returns the result the guard stored, if any.
func ResultFrom(ctx context.Context) (*Result, bool) {
	res, ok := ctx.Value(resultKey{}).(*Result)
	return res, ok
}
