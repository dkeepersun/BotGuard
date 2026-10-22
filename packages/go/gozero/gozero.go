// Package botguardgozero adapts the BotGuard Go SDK to go-zero.
//
//	client, _ := botguard.New(c.BotGuard.Endpoint, c.BotGuard.SecretID, c.BotGuard.SecretKey,
//		botguard.WithAppKey(c.BotGuard.AppKey))
//	login := botguardgozero.Middleware(botguard.NewGuard(client, "login"))
//	server.AddRoutes(rest.WithMiddlewares([]rest.Middleware{login}, loginRoutes...))
//
// In the handler, botguard.ResultFrom(r.Context()) returns the passed result.
package botguardgozero

import (
	"net/http"
	"strconv"
	"time"

	botguard "github.com/dkeepersun/BotGuard/packages/go"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// RejectedError is what UseErrorHandler hands to httpx.ErrorCtx for a
// rejected token; the *botguard.CallerError and *botguard.UnknownError
// failures are passed as they are.
type RejectedError struct{ Result *botguard.Result }

func (e *RejectedError) Error() string { return "botguard: token rejected: " + e.Result.Reason }

// Option configures Middleware.
type Option func(*options)

type options struct{ errorHandler bool }

// UseErrorHandler sends rejections (as *RejectedError) and failures through
// httpx.ErrorCtx, so the error handler set with httpx.SetErrorHandlerCtx
// decides the response like for any other error of the service. Failures
// are still logged first.
func UseErrorHandler() Option { return func(o *options) { o.errorHandler = true } }

// Middleware lets the request through only when its token passes. By
// default rejections and failures are answered with httpx.WriteJsonCtx and
// the SDK's statuses: 403 BOTGUARD_REJECTED, 500 BOTGUARD_MISCONFIGURED,
// 503 BOTGUARD_UNAVAILABLE.
func Middleware(g *botguard.Guard, opts ...Option) rest.Middleware {
	var o options
	for _, f := range opts {
		f(&o)
	}
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			res, err := g.Check(r)
			switch {
			case err != nil:
				g.Log(r, err)
				if o.errorHandler {
					httpx.ErrorCtx(r.Context(), w, err)
					return
				}
				status, body, after := botguard.ErrorResponse(err)
				if after > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(int(after/time.Second)))
				}
				httpx.WriteJsonCtx(r.Context(), w, status, body)
			case !res.Passed:
				if o.errorHandler {
					httpx.ErrorCtx(r.Context(), w, &RejectedError{Result: res})
					return
				}
				status, body := botguard.RejectResponse(res)
				httpx.WriteJsonCtx(r.Context(), w, status, body)
			default:
				next(w, r.WithContext(botguard.WithResult(r.Context(), res)))
			}
		}
	}
}
