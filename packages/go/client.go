// Package botguard is the Go server-side SDK for BotGuard: it verifies the
// one-time token the browser widget puts into a form, like Yidun's
// NECaptchaVerifier. The business server holds secret_id/secret_key; the
// browser only ever sees the app_key and its token.
//
//	client, err := botguard.New("https://botguard.example.com", secretID, secretKey,
//		botguard.WithAppKey("app_xxx"))
//	res, err := client.VerifyToken(ctx, r.PostFormValue("botguard_token"), "login")
//	switch {
//	case err != nil: // *CallerError (fix config) or *UnknownError (retry later); do not proceed
//	case !res.Passed: // rejected: ask the user to verify again (res.Reason)
//	default: // passed: run the business operation
//	}
//
// Outcomes follow api/contract-v1.md: Passed and Rejected are returned as a
// *Result; caller errors and unknown outcomes are returned as *CallerError
// and *UnknownError. Only the standard library is used.
package botguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultTimeout bounds one attempt when no timeout is given.
	DefaultTimeout = 3 * time.Second
	// MinTimeout is the floor for any timeout, so a request is never left
	// effectively unbounded or impossibly short.
	MinTimeout = 50 * time.Millisecond
	// maxResponse bounds the response body; anything larger breaks the contract.
	maxResponse = 64 << 10
)

// Local format checks, mirroring the gateway (internal/domain).
var (
	appKeyPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	actionPattern    = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// Tokens are opaque to callers, but every token the gateway issues is
	// base64url text joined by dots and at most 256 bytes; anything else is
	// TOKEN_INVALID on the gateway too, so it is rejected without a request.
	tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,256}$`)
)

// Client verifies tokens against one gateway with one secret pair. It is
// safe for concurrent use.
type Client struct {
	url       string
	secretID  string
	secretKey []byte
	appKey    string
	retries   int
	timeout   time.Duration
	http      *http.Client
	now       func() time.Time
}

// Option configures a Client.
type Option func(*Client)

// WithAppKey sets the default app for VerifyToken and the HTTP guard.
func WithAppKey(appKey string) Option { return func(c *Client) { c.appKey = appKey } }

// WithTimeout bounds each attempt (default 3s; values below 50ms are raised
// to 50ms). A deadline on the context still applies on top.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// WithRetries makes up to n extra attempts after a network error or 5xx,
// each re-signed with the same request_id and body, so a retry gets the
// idempotent result rather than consuming the token twice. 429 and protocol
// errors are never retried here.
func WithRetries(n int) Option { return func(c *Client) { c.retries = max(0, n) } }

// WithHTTPClient uses hc's transport (proxies, TLS settings, tracing). The
// client is copied: redirects are never followed, and WithTimeout (or hc's
// own Timeout, or the default) always bounds each attempt.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			cp := *hc
			c.http = &cp
		}
	}
}

func clampTimeout(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return DefaultTimeout
	case d < MinTimeout:
		return MinTimeout
	default:
		return d
	}
}

// New returns a client for the gateway at endpoint, e.g.
// https://botguard.example.com. secretKey comes from server configuration
// and must never reach a browser.
func New(endpoint, secretID, secretKey string, opts ...Option) (*Client, error) {
	if secretID == "" || secretKey == "" {
		return nil, errors.New("botguard: secret_id 和 secret_key 不能为空")
	}
	if !strings.HasPrefix(endpoint, "https://") && !strings.HasPrefix(endpoint, "http://") {
		return nil, fmt.Errorf("botguard: endpoint 必须以 http:// 或 https:// 开头：%q", endpoint)
	}
	c := &Client{
		url:       strings.TrimRight(endpoint, "/") + verifyPath,
		secretID:  secretID,
		secretKey: []byte(secretKey),
		http:      &http.Client{},
		now:       time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	if c.timeout == 0 {
		c.timeout = c.http.Timeout
	}
	c.http.Timeout = clampTimeout(c.timeout)
	// A redirect would resend nothing useful and could leak the request
	// elsewhere; the 3xx is reported as a caller error instead.
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c, nil
}

// String keeps the secret key out of logs and debug output.
func (c *Client) String() string {
	return fmt.Sprintf("botguard.Client{url: %s, secretID: %s, secretKey: [REDACTED], appKey: %s}", c.url, c.secretID, c.appKey)
}

// GoString is used by %#v.
func (c *Client) GoString() string { return c.String() }

// AppKey is the default app set with WithAppKey.
func (c *Client) AppKey() string { return c.appKey }

// VerifyToken is the one-line form, like Yidun's verify(validate): it uses
// the app key from WithAppKey and a fresh request_id. When you need to retry
// after an unknown result, call Verify with a request_id derived from your
// own operation instead.
func (c *Client) VerifyToken(ctx context.Context, token, action string) (*Result, error) {
	if c.appKey == "" {
		return nil, &CallerError{Code: "NOT_CONFIGURED", Message: "VerifyToken 需要在 New 时传入 WithAppKey"}
	}
	return c.Verify(ctx, VerifyRequest{AppKey: c.appKey, Token: token, Action: action, RequestID: NewRequestID()})
}

// Verify checks one token. A malformed app_key, action or request_id is the
// caller's to fix and returns a *CallerError (INVALID_REQUEST) before
// anything is sent. A malformed token comes from the browser and is a local
// rejection with reason TOKEN_INVALID, as the gateway would answer.
func (c *Client) Verify(ctx context.Context, req VerifyRequest) (*Result, error) {
	if err := checkCallerFields(req); err != nil {
		return nil, err
	}
	if !tokenPattern.MatchString(req.Token) {
		return &Result{Reason: ReasonTokenInvalid, RequestID: req.RequestID}, nil
	}
	body, err := json.Marshal(map[string]string{
		"app_key": req.AppKey, "token": req.Token, "action": req.Action, "request_id": req.RequestID,
	})
	if err != nil {
		return nil, &CallerError{Code: "INVALID_REQUEST", Message: err.Error(), RequestID: req.RequestID}
	}
	for attempt := 0; ; attempt++ {
		res, err := c.attempt(ctx, req, body)
		var unknown *UnknownError
		if err == nil || !errors.As(err, &unknown) || unknown.Protocol || unknown.Status == http.StatusTooManyRequests ||
			attempt >= c.retries || ctx.Err() != nil {
			return res, err
		}
	}
}

func checkCallerFields(req VerifyRequest) error {
	var bad string
	switch {
	case !appKeyPattern.MatchString(req.AppKey):
		bad = "app_key 格式错误：应为 [A-Za-z0-9_-]，8–64 个字符"
	case !actionPattern.MatchString(req.Action):
		bad = "action 格式错误：应为 [a-z0-9_]，1–32 个字符，并由后端写死"
	case !requestIDPattern.MatchString(req.RequestID):
		bad = "request_id 格式错误：应为 [A-Za-z0-9_-]，1–64 个字符"
	default:
		return nil
	}
	e := &CallerError{Code: "INVALID_REQUEST", Message: "请求未发送，" + bad}
	// request_id is echoed back only when it is well-formed.
	if requestIDPattern.MatchString(req.RequestID) {
		e.RequestID = req.RequestID
	}
	return e
}

// envelope is the gateway's response: {"code","message","data","request_id"}.
type envelope struct {
	Code    *string         `json:"code"`
	Message *string         `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) attempt(ctx context.Context, req VerifyRequest, body []byte) (*Result, error) {
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, &CallerError{Code: "INVALID_REQUEST", Message: "endpoint 无效：" + err.Error(), RequestID: req.RequestID}
	}
	hr.Header.Set("Content-Type", "application/json")
	// Fresh timestamp and nonce per attempt; body and request_id unchanged.
	signHeaders(hr.Header, c.secretID, c.secretKey, body, c.now().Unix(), newNonce())
	resp, err := c.http.Do(hr)
	if err != nil {
		return nil, &UnknownError{Message: "网络错误", RequestID: req.RequestID, Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	status := resp.StatusCode
	if err != nil {
		return nil, &UnknownError{Status: status, Message: "读取响应失败", RequestID: req.RequestID, Err: err}
	}
	if len(raw) > maxResponse {
		// Retrying would only fetch the same oversized body again.
		return nil, &UnknownError{Status: status, Message: "响应体超过大小上限", RequestID: req.RequestID, Protocol: true}
	}

	var env envelope
	parsed := json.Unmarshal(raw, &env) == nil
	code, message := "", fmt.Sprintf("HTTP %d", status)
	if parsed && env.Code != nil {
		code = *env.Code
	}
	if parsed && env.Message != nil {
		message = *env.Message
	}

	switch {
	case status == http.StatusOK:
		return parseOK(req, parsed, env)
	case status == http.StatusTooManyRequests || (status >= 500 && status <= 599):
		return nil, &UnknownError{Status: status, Code: code, Message: message, RequestID: req.RequestID,
			RetryAfter: retryAfter(resp.Header.Get("Retry-After"), c.now())}
	case status >= 300 && status <= 499:
		// Redirects, 404/405 (wrong endpoint), 413/415 and the contract's
		// 400/401/403/409: retrying the same request cannot succeed.
		if code == "" {
			message = fmt.Sprintf("BotGuard 返回 HTTP %d，请检查 endpoint 与网关配置", status)
		}
		return nil, &CallerError{Status: status, Code: code, Message: message, RequestID: req.RequestID}
	default:
		return nil, &UnknownError{Status: status, Code: code, Message: fmt.Sprintf("返回了契约之外的 HTTP %d", status),
			RequestID: req.RequestID, Protocol: true}
	}
}

func parseOK(req VerifyRequest, parsed bool, env envelope) (*Result, error) {
	protocol := func(msg string) error {
		return &UnknownError{Status: http.StatusOK, Message: msg, RequestID: req.RequestID, Protocol: true}
	}
	var data struct {
		Success       *bool           `json:"success"`
		Reason        *string         `json:"reason"`
		AppKey        *string         `json:"app_key"`
		Action        *string         `json:"action"`
		VerifiedAt    json.RawMessage `json:"verified_at"`
		ChallengeKind any             `json:"challenge_kind"`
		RiskLevel     any             `json:"risk_level"`
		RiskReasons   any             `json:"risk_reasons"`
		Origin        any             `json:"origin"`
	}
	if !parsed || env.Code == nil || *env.Code != "OK" || json.Unmarshal(env.Data, &data) != nil || data.Success == nil {
		return nil, protocol("响应格式不符合契约")
	}
	if !*data.Success {
		if data.Reason == nil || *data.Reason == "" {
			return nil, protocol("拒绝结果缺少 reason")
		}
		return &Result{Reason: *data.Reason, RequestID: req.RequestID}, nil
	}
	// A pass for a different app or action is not a pass for this request.
	verifiedAt, err := strconv.ParseInt(string(data.VerifiedAt), 10, 64)
	if data.AppKey == nil || *data.AppKey != req.AppKey || data.Action == nil || *data.Action != req.Action || err != nil {
		return nil, protocol("通过结果与请求的 app_key/action 不符")
	}
	// Risk fields are additive: absent on older gateways, never trusted to
	// change the outcome, and dropped when not of the expected type.
	res := &Result{Passed: true, AppKey: req.AppKey, Action: req.Action, RequestID: req.RequestID,
		VerifiedAt: time.Unix(verifiedAt, 0), RiskReasons: []string{}}
	res.ChallengeKind, _ = data.ChallengeKind.(string)
	res.RiskLevel, _ = data.RiskLevel.(string)
	res.Origin, _ = data.Origin.(string)
	if list, ok := data.RiskReasons.([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok {
				res.RiskReasons = append(res.RiskReasons, s)
			}
		}
	}
	return res, nil
}

// retryAfter reads Retry-After as delay-seconds or an HTTP-date (RFC 9110
// §10.2.3); zero when absent or unparseable, or for a date in the past.
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.ParseUint(v, 10, 32); err == nil {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now).Truncate(time.Second)
	}
	return 0
}
