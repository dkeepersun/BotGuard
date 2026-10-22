package botguard

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	// accept lists the step-up reasons let through (AllowFallback & co.).
	accept map[string]bool
	// origins, when set, are the page origins a pass must come from.
	origins []string
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

// AllowFallback lets tokens earned on the accessible fallback through. By
// default they are rejected with ReasonStepUpFallback: the fallback is proof
// of work only. Allow it when the business adds its own check for such
// requests, or values accessibility over the cost to bots.
func AllowFallback() GuardOption { return allow(ReasonStepUpFallback) }

// AllowDegraded lets tokens earned while the gateway had no image capacity
// (CAPACITY_DEGRADED, proof of work only) through. By default they are
// rejected with ReasonStepUpDegraded; allowing them keeps the action usable
// under an image-quota flood at the price of the cheaper path.
func AllowDegraded() GuardOption { return allow(ReasonStepUpDegraded) }

// AllowHighRisk lets high-risk tokens through. By default they are rejected
// with ReasonStepUpHighRisk, although the client solved the hardest kind.
func AllowHighRisk() GuardOption { return allow(ReasonStepUpHighRisk) }

func allow(reason string) GuardOption {
	return func(g *Guard) {
		if g.accept == nil {
			g.accept = map[string]bool{}
		}
		g.accept[reason] = true
	}
}

// WithAllowedOrigins lets a pass through only when the token was earned on
// one of these page origins: exact ones such as https://shop.example.com, or
// https://*.example.com for any subdomain (not example.com itself), matched
// like the gateway's allowed origins. Use it when the app's allowed origins
// in the gateway use a wildcard or *, so one app cannot stand in for another
// site. Any other pass is rejected with ReasonOriginNotAllowed, including one
// from a gateway too old to report the origin. NewGuard panics on a pattern
// that is not scheme://host[:port] or scheme://*.host[:port].
func WithAllowedOrigins(origins ...string) GuardOption {
	return func(g *Guard) { g.origins = append(g.origins, origins...) }
}

// validOriginPattern accepts what WithAllowedOrigins documents, already
// lower-case and without a trailing slash.
func validOriginPattern(p string) bool {
	u, err := url.Parse(strings.Replace(p, "://*.", "://x.", 1))
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" &&
		u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil &&
		strings.Count(p, "*") <= 1 && (!strings.Contains(p, "*") || strings.Contains(p, "://*."))
}

// originAllowed matches origin against the patterns as the gateway does:
// a pattern with "://*." matches any origin with that scheme and a longer
// host ending in the rest.
func originAllowed(patterns []string, origin string) bool {
	origin = strings.ToLower(origin)
	for _, p := range patterns {
		scheme, suffix, wild := strings.Cut(p, "://*.")
		if !wild {
			if p == origin {
				return true
			}
			continue
		}
		prefix, suffix := scheme+"://", "."+suffix
		if len(origin) > len(prefix)+len(suffix) && strings.HasPrefix(origin, prefix) && strings.HasSuffix(origin, suffix) {
			return true
		}
	}
	return false
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
	for i, o := range g.origins {
		o = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(o)), "/")
		if !validOriginPattern(o) {
			panic("botguard: WithAllowedOrigins 的来源格式错误：" + g.origins[i] + "（应为 https://host[:port] 或 https://*.host[:port]）")
		}
		g.origins[i] = o
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
// rejected) or a *CallerError / *UnknownError, like Client.Verify.
//
// A pass from an origin outside WithAllowedOrigins comes back rejected with
// ReasonOriginNotAllowed. A pass that needs a step-up (Result.StepUp) and is
// not allowed by AllowFallback, AllowDegraded or AllowHighRisk comes back
// rejected with the step-up reason. Either keeps the gateway's risk fields
// for OnReject.
func (g *Guard) Check(r *http.Request) (*Result, error) {
	res, err := g.client.Verify(r.Context(), VerifyRequest{AppKey: g.appKey, Token: g.tokenFrom(r),
		Action: g.action, RequestID: g.requestID(r)})
	if err != nil {
		return nil, err
	}
	if res.Passed && len(g.origins) > 0 && !originAllowed(g.origins, res.Origin) {
		held := *res
		held.Passed, held.Reason = false, ReasonOriginNotAllowed
		return &held, nil
	}
	if why := res.StepUp(); why != "" && !g.accept[why] {
		held := *res
		held.Passed, held.Reason = false, why
		return &held, nil
	}
	return res, nil
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
// A step-up rejection answers BOTGUARD_STEP_UP instead, so the page can offer
// the extra check rather than another captcha.
func RejectResponse(res *Result) (status int, body map[string]string) {
	if IsStepUp(res.Reason) {
		return http.StatusForbidden, map[string]string{"code": "BOTGUARD_STEP_UP", "reason": res.Reason, "message": "需要进一步验证"}
	}
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
