// Package errx carries the contract error code and HTTP status from logic to
// handlers. Errors that are not *Error are treated as 503 UNAVAILABLE.
package errx

import "net/http"

type Error struct {
	Status     int
	Code       string
	Message    string
	RetryAfter int
}

func (e *Error) Error() string { return e.Code }

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func InvalidRequest(message string) *Error {
	return New(http.StatusBadRequest, "INVALID_REQUEST", message)
}

func RateLimited(retryAfter int) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: "请求过于频繁，请稍后再试", RetryAfter: retryAfter}
}

var (
	ErrChallengeInvalid = New(http.StatusBadRequest, "CHALLENGE_INVALID", "验证数据无效，请重试")
	ErrChallengeExpired = New(http.StatusGone, "CHALLENGE_EXPIRED", "验证已过期，请重试")
	ErrChallengeUsed    = New(http.StatusConflict, "CHALLENGE_USED", "验证已提交，请重试")
	ErrProofInvalid     = New(http.StatusUnprocessableEntity, "PROOF_INVALID", "验证计算结果无效，请重试")
	ErrOriginNotAllowed = New(http.StatusForbidden, "ORIGIN_NOT_ALLOWED", "当前来源未被允许")
	ErrSiteForbidden    = New(http.StatusForbidden, "SITE_FORBIDDEN", "站点不可用")
	ErrActionForbidden  = New(http.StatusForbidden, "ACTION_FORBIDDEN", "该操作未启用验证")
	ErrUnauthorized     = New(http.StatusUnauthorized, "UNAUTHORIZED", "服务端凭据无效")
	ErrIdempotency      = New(http.StatusConflict, "IDEMPOTENCY_CONFLICT", "request_id 已用于不同的请求")
	ErrUnavailable      = New(http.StatusServiceUnavailable, "UNAVAILABLE", "验证服务暂不可用，请稍后再试")
)
