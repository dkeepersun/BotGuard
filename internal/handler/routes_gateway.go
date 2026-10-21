package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/dkeepersun/BotGuard/internal/errx"
	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/response"
	"github.com/dkeepersun/BotGuard/internal/sdkassets"
	"github.com/dkeepersun/BotGuard/internal/service/clientip"
	"github.com/dkeepersun/BotGuard/internal/service/risk"
	"github.com/dkeepersun/BotGuard/internal/service/signature"
	"github.com/zeromicro/go-zero/rest"
)

const (
	maxBody      = 8 << 10
	maxSolveBody = 32 << 10 // slider trajectories
)

func Health(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

// Readiness is the admin preview's probe: it has no stores to check and must
// never report ready.
func Readiness(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "reason": "preview_only"})
}

type Gateway struct {
	Logic *logic.Gateway
	IPs   *clientip.Resolver
	// Ready returns "" when ready, otherwise a reason code for the probe.
	Ready func(ctx context.Context) string
}

func (g *Gateway) Register(s *rest.Server) {
	s.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/healthz", Handler: Health},
		{Method: http.MethodGet, Path: "/readyz", Handler: g.readiness},
		{Method: http.MethodPost, Path: "/v1/challenges", Handler: g.challenges},
		{Method: http.MethodOptions, Path: "/v1/challenges", Handler: preflight},
		{Method: http.MethodPost, Path: "/v1/solutions", Handler: g.solutions},
		{Method: http.MethodOptions, Path: "/v1/solutions", Handler: preflight},
		{Method: http.MethodPost, Path: "/v1/verify", Handler: g.verify},
	})
	for _, name := range sdkassets.Files() {
		s.AddRoute(rest.Route{Method: http.MethodGet, Path: "/sdk/v1/" + name, Handler: sdkassets.Handler(name)})
	}
}

func (g *Gateway) readiness(w http.ResponseWriter, r *http.Request) {
	if reason := g.Ready(r.Context()); reason != "" {
		response.JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "reason": reason})
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// decode accepts application/json and text/plain bodies. text/plain lets the
// browser widget send a CORS "simple request" and skip the preflight.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	return decodeLimit(w, r, v, maxBody)
}

func decodeLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	body, err := readBodyLimit(w, r, limit)
	if err != nil {
		return err
	}
	return decodeBytes(body, v)
}

// readBody returns the raw bytes, which verify needs for the signature.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	return readBodyLimit(w, r, maxBody)
}

func readBodyLimit(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (mt != "application/json" && mt != "text/plain") {
		return nil, errx.InvalidRequest("Content-Type 必须为 application/json 或 text/plain")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return nil, errx.InvalidRequest("请求体过大或读取失败")
	}
	return body, nil
}

func decodeBytes(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(v); err != nil {
		return errx.InvalidRequest("请求体不是合法 JSON")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errx.InvalidRequest("请求体只能包含一个 JSON 对象")
	}
	return nil
}

func fail(w http.ResponseWriter, err error, requestID string) {
	var e *errx.Error
	if !errors.As(err, &e) {
		e = errx.ErrUnavailable
	}
	response.Fail(w, e.Status, e.Code, e.Message, requestID, e.RetryAfter)
}

// HeaderAppOrigin carries the app://<package name> origin of a native
// client (e.g. a uni-app App sending through its logic layer), which has no
// Origin header of its own.
const HeaderAppOrigin = "X-BotGuard-Origin"

// requestOrigin is the browser's Origin. Only when there is none, i.e. the
// caller is not a browser (browsers send Origin with every POST, "null"
// included), does the app origin header count, so a web page cannot claim
// an app's origin.
func requestOrigin(r *http.Request) string {
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin
	}
	return r.Header.Get(HeaderAppOrigin)
}

// cors is only ever called with an origin already allowed for the app.
// App origins come from native clients, which do not use CORS.
func cors(w http.ResponseWriter, origin string) {
	w.Header().Add("Vary", "Origin")
	if origin != "" && !strings.HasPrefix(origin, "app://") {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
}

// preflight cannot know the app (no body), so it answers any origin. The
// real request is still checked against the app's allowlist, and its
// response only carries CORS headers for allowed origins.
func preflight(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Origin")
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) challenges(w http.ResponseWriter, r *http.Request) {
	rid := newRequestID()
	var body struct {
		AppKey   string        `json:"app_key"`
		Action   string        `json:"action"`
		Signals  *risk.Signals `json:"signals"`
		Fallback bool          `json:"fallback"`
	}
	if err := decode(w, r, &body); err != nil {
		fail(w, err, rid)
		return
	}
	out, origin, err := g.Logic.Issue(r.Context(), logic.IssueInput{
		AppKey: body.AppKey, Action: body.Action, Origin: requestOrigin(r), ClientIP: g.IPs.Resolve(r),
		HeaderUA: r.UserAgent(), Signals: body.Signals, Fallback: body.Fallback,
	})
	cors(w, origin)
	if err != nil {
		fail(w, err, rid)
		return
	}
	response.OK(w, out, rid)
}

func (g *Gateway) solutions(w http.ResponseWriter, r *http.Request) {
	rid := newRequestID()
	var body struct {
		AppKey    string              `json:"app_key"`
		Challenge string              `json:"challenge"`
		Nonces    []string            `json:"nonces"`
		Answer    *logic.SliderAnswer `json:"answer"`
		Signals   *risk.Signals       `json:"signals"`
	}
	if err := decodeLimit(w, r, &body, maxSolveBody); err != nil {
		fail(w, err, rid)
		return
	}
	out, origin, err := g.Logic.Solve(r.Context(), logic.SolveInput{
		AppKey: body.AppKey, Challenge: body.Challenge, Nonces: body.Nonces,
		Origin: requestOrigin(r), ClientIP: g.IPs.Resolve(r), Answer: body.Answer, Signals: body.Signals,
		HeaderUA: r.UserAgent(),
	})
	cors(w, origin)
	if err != nil {
		fail(w, err, rid)
		return
	}
	response.OK(w, out, rid)
}

func (g *Gateway) verify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AppKey    string `json:"app_key"`
		Token     string `json:"token"`
		Action    string `json:"action"`
		RequestID string `json:"request_id"`
	}
	raw, err := readBody(w, r)
	if err == nil {
		err = decodeBytes(raw, &body)
	}
	if err != nil {
		fail(w, err, "")
		return
	}
	out, err := g.Logic.Verify(r.Context(), logic.VerifyInput{
		ClientIP:  g.IPs.Resolve(r),
		SecretID:  r.Header.Get(signature.HeaderSecretID),
		Timestamp: r.Header.Get(signature.HeaderTimestamp),
		Nonce:     r.Header.Get(signature.HeaderNonce),
		Signature: r.Header.Get(signature.HeaderSignature),
		Body:      raw,
		AppKey:    body.AppKey, Token: body.Token, Action: body.Action, RequestID: body.RequestID,
	})
	if err != nil {
		fail(w, err, body.RequestID)
		return
	}
	response.OK(w, out, body.RequestID)
}
