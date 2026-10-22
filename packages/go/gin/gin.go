// Package botguardgin adapts the BotGuard Go SDK to gin.
//
//	client, _ := botguard.New(endpoint, secretID, secretKey, botguard.WithAppKey("app_xxx"))
//	r.POST("/login", botguardgin.Protect(botguard.NewGuard(client, "login")), loginHandler)
//
//	func loginHandler(c *gin.Context) {
//		res, _ := botguardgin.Result(c) // risk level, challenge kind ...
//	}
package botguardgin

import (
	"strconv"
	"time"

	botguard "github.com/dkeepersun/BotGuard/packages/go"
	"github.com/gin-gonic/gin"
)

// ContextKey is where Protect stores the passed *botguard.Result in the gin
// context; the request context carries it too (botguard.ResultFrom).
const ContextKey = "botguard.result"

// Option configures Protect.
type Option func(*options)

type options struct {
	onReject func(*gin.Context, *botguard.Result)
	onError  func(*gin.Context, error)
}

// OnReject replaces the response for a rejected token (default: abort with
// 403 {"code":"BOTGUARD_REJECTED","reason":...,"message":...}). It must
// abort the context.
func OnReject(f func(*gin.Context, *botguard.Result)) Option {
	return func(o *options) { o.onReject = f }
}

// OnError replaces the response for a *botguard.CallerError or
// *botguard.UnknownError (default: log it, then abort with 500
// BOTGUARD_MISCONFIGURED or 503 BOTGUARD_UNAVAILABLE). It must abort the
// context.
func OnError(f func(*gin.Context, error)) Option {
	return func(o *options) { o.onError = f }
}

// Protect lets the request through only when its token passes, reading the
// token, request_id and app key the way the guard was configured.
func Protect(g *botguard.Guard, opts ...Option) gin.HandlerFunc {
	o := options{
		onReject: func(c *gin.Context, res *botguard.Result) {
			status, body := botguard.RejectResponse(res)
			c.AbortWithStatusJSON(status, body)
		},
		onError: func(c *gin.Context, err error) {
			g.Log(c.Request, err)
			status, body, after := botguard.ErrorResponse(err)
			if after > 0 {
				c.Header("Retry-After", strconv.Itoa(int(after/time.Second)))
			}
			c.AbortWithStatusJSON(status, body)
		},
	}
	for _, f := range opts {
		f(&o)
	}
	return func(c *gin.Context) {
		res, err := g.Check(c.Request)
		switch {
		case err != nil:
			o.onError(c, err)
		case !res.Passed:
			o.onReject(c, res)
		default:
			c.Set(ContextKey, res)
			c.Request = c.Request.WithContext(botguard.WithResult(c.Request.Context(), res))
			c.Next()
		}
	}
}

// Result returns the passed result Protect stored.
func Result(c *gin.Context) (*botguard.Result, bool) {
	v, ok := c.Get(ContextKey)
	if !ok {
		return nil, false
	}
	res, ok := v.(*botguard.Result)
	return res, ok
}
