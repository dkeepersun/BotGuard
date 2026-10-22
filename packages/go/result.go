package botguard

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Reason codes of a rejected token (api/contract-v1.md). The gateway may add
// new ones; any reason, known or not, is still a rejection.
const (
	ReasonTokenInvalid     = "TOKEN_INVALID"
	ReasonTokenExpired     = "TOKEN_EXPIRED"
	ReasonTokenConsumed    = "TOKEN_CONSUMED"
	ReasonAppMismatch      = "APP_MISMATCH"
	ReasonActionMismatch   = "ACTION_MISMATCH"
	ReasonOriginNotAllowed = "ORIGIN_NOT_ALLOWED"
)

// VerifyRequest is one verify call. Keep RequestID stable for the whole
// business operation: retries after an unknown result must reuse it, never
// mint a new one.
type VerifyRequest struct {
	AppKey string
	// Token is whatever the browser submitted, e.g. the botguard_token field.
	Token string
	// Action is fixed by the handler, never taken from the browser.
	Action    string
	RequestID string
}

// NewRequestID returns a UUID v4, suitable as a request_id.
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("botguard: crypto/rand: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Result is Passed or Rejected. Caller errors and unknown outcomes are
// errors instead, so a misconfiguration can never be mistaken for "the user
// failed the check".
type Result struct {
	// Passed is the only outcome that may let the business operation run.
	Passed bool
	// Reason is the contract reason code when rejected, e.g. TOKEN_EXPIRED.
	Reason    string
	AppKey    string
	Action    string
	RequestID string
	// VerifiedAt is set when passed.
	VerifiedAt time.Time
	// How the token was earned: invisible, slider, click_text, fallback ...
	// Empty on older gateways. These risk fields never change the outcome;
	// they let the business ask for more (an SMS code, say) on high risk.
	ChallengeKind string
	// RiskLevel is low, medium or high.
	RiskLevel string
	// RiskReasons are codes such as NO_SIGNALS or CAPACITY_DEGRADED.
	RiskReasons []string
}

// Rejected reports whether the token was refused; ask the user to verify again.
func (r *Result) Rejected() bool { return !r.Passed }

// CallerError is a mistake the caller has to fix in code or configuration:
// a malformed app_key, action or request_id (Status 0, nothing sent), or any
// 3xx/4xx but 429 (bad secret, clock skew, disabled app, wrong endpoint ...).
// Retrying the same request cannot succeed.
type CallerError struct {
	// Status is the HTTP status, 0 when the request was not sent.
	Status int
	// Code is the contract error code, e.g. UNAUTHORIZED; empty when the
	// response carried none.
	Code      string
	Message   string
	RequestID string
}

func (e *CallerError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("botguard: caller error %s (HTTP %d): %s", e.Code, e.Status, e.Message)
	}
	return fmt.Sprintf("botguard: caller error (HTTP %d): %s", e.Status, e.Message)
}

// UnknownError means the outcome is unknown: 429, 5xx, a network error, a
// timeout, or a response outside the contract. The gateway may already have
// consumed the token, so do not let the operation run; retry later with the
// same RequestID, or ask the user to try again.
type UnknownError struct {
	// Status is the HTTP status, 0 when no response arrived.
	Status    int
	Code      string
	Message   string
	RequestID string
	// RetryAfter comes from a 429 or 503 Retry-After header; zero if absent.
	RetryAfter time.Duration
	// Protocol is set when the response broke the contract (malformed body,
	// oversized body, a pass for another app or action, an unexpected
	// status). Worth logging separately; retrying will not help.
	Protocol bool
	// Err is the underlying network error, if any.
	Err error
}

func (e *UnknownError) Error() string {
	switch {
	case e.Err != nil:
		return "botguard: unknown result: " + e.Message + ": " + e.Err.Error()
	case e.Code != "":
		return fmt.Sprintf("botguard: unknown result %s (HTTP %d): %s", e.Code, e.Status, e.Message)
	default:
		return fmt.Sprintf("botguard: unknown result (HTTP %d): %s", e.Status, e.Message)
	}
}

func (e *UnknownError) Unwrap() error { return e.Err }

// IsCallerError reports whether err is (or wraps) a *CallerError.
func IsCallerError(err error) bool {
	var e *CallerError
	return errors.As(err, &e)
}

// IsUnknown reports whether err is (or wraps) an *UnknownError.
func IsUnknown(err error) bool {
	var e *UnknownError
	return errors.As(err, &e)
}
