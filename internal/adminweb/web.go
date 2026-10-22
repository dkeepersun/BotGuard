package adminweb

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/zeromicro/go-zero/core/logx"
)

const (
	sessionCookie = "bg_admin"
	flashCookie   = "bg_flash"
)

type principalKey struct{}
type nonceKey struct{}

// Flash is a one-shot message shown after a redirect.
type Flash struct {
	Kind string `json:"k"` // ok or error
	Text string `json:"t"`
}

// View is what every page template receives.
type View struct {
	Title  string
	Active string // menu id
	Nonce  string
	CSRF   string
	Me     *logic.Principal
	Flash  *Flash
	Data   any
}

func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawStdEncoding.EncodeToString(b)
}

// secure sets the response headers every console response carries. Pages
// are framed by the PearAdmin shell from the same origin only. Inline
// scripts need the per-request nonce; Layui needs inline styles. No eval:
// the bundled jQuery runs <script> found in inserted markup through eval, so
// allowing it would turn any markup injection into script execution.
func (s *Server) secure(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nonce := newNonce()
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-"+nonce+"'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-src 'self'; frame-ancestors 'self'; form-action 'self'; base-uri 'none'; object-src 'none'")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next(w, r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce)))
	}
}

func nonceOf(r *http.Request) string { s, _ := r.Context().Value(nonceKey{}).(string); return s }

func principalOf(r *http.Request) *logic.Principal {
	p, _ := r.Context().Value(principalKey{}).(*logic.Principal)
	return p
}

func (s *Server) clientIP(r *http.Request) string {
	if s.IPs == nil {
		host := r.RemoteAddr
		if i := strings.LastIndexByte(host, ':'); i > 0 {
			host = host[:i]
		}
		return strings.Trim(host, "[]")
	}
	if a := s.IPs.Resolve(r); a.IsValid() {
		return a.String()
	}
	return ""
}

func (s *Server) setSessionCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: s.CookieSecure,
		SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}

func (s *Server) setFlash(w http.ResponseWriter, kind, text string) {
	b, _ := json.Marshal(Flash{Kind: kind, Text: text})
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: base64.RawURLEncoding.EncodeToString(b), Path: "/", HttpOnly: true,
		Secure: s.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: 60})
}

func (s *Server) takeFlash(w http.ResponseWriter, r *http.Request) *Flash {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteStrictMode})
	b, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return nil
	}
	var f Flash
	if json.Unmarshal(b, &f) != nil || f.Text == "" {
		return nil
	}
	return &f
}

// authenticate loads the principal from the session cookie.
func (s *Server) authenticate(r *http.Request) (*logic.Principal, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, nil
	}
	return s.Console.Authenticate(r.Context(), c.Value)
}

// page wraps a GET handler: signed in, allowed, and sent to change the
// password first when the account still has a generated one.
func (s *Server) page(perm string, h func(w http.ResponseWriter, r *http.Request, p *logic.Principal)) http.HandlerFunc {
	return s.secure(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.authenticate(r)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if p == nil {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		if p.Admin.MustChangePassword && r.URL.Path != "/profile" && r.URL.Path != "/" && r.URL.Path != "/menus" && r.URL.Path != "/pear-config" {
			http.Redirect(w, r, "/profile?force=1", http.StatusSeeOther)
			return
		}
		if perm != "" && !p.Can(perm) {
			w.WriteHeader(http.StatusForbidden)
			s.render(w, r, "forbidden.html", View{Title: "没有权限", Me: p})
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)), p)
	})
}

// sameOrigin rejects cross-site form posts: the browser's Origin (or, when
// absent, Sec-Fetch-Site) must name this console.
func sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	site := r.Header.Get("Sec-Fetch-Site")
	return site == "" || site == "same-origin"
}

// returnPath is where an action sends the browser afterwards: an explicit
// _back field, else the same-origin page the form was on. Never the action
// URL itself, which has no GET route.
func returnPath(r *http.Request) string {
	if back := r.PostFormValue("_back"); strings.HasPrefix(back, "/") && !strings.HasPrefix(back, "//") {
		return back
	}
	if ref, err := url.Parse(r.Referer()); err == nil && ref.Host == r.Host && strings.HasPrefix(ref.Path, "/") {
		return ref.RequestURI()
	}
	return "/console"
}

// errRedirect is returned by actions that want a specific page afterwards.
type errRedirect struct{ to string }

func (e errRedirect) Error() string { return "redirect " + e.to }

// action wraps a POST handler: signed in, same origin, CSRF token, allowed.
// The handler returns a flash message; errors from logic.ErrInvalid are shown
// to the admin, anything else is logged and shown generically.
func (s *Server) action(perm string, h func(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error)) http.HandlerFunc {
	return s.secure(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if !sameOrigin(r) {
			http.Error(w, "跨站请求被拒绝", http.StatusForbidden)
			return
		}
		p, err := s.authenticate(r)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if p == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "请求格式错误", http.StatusBadRequest)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.PostFormValue("_csrf")), []byte(p.Session.CSRF)) != 1 {
			http.Error(w, "页面已过期，请刷新后重试", http.StatusForbidden)
			return
		}
		if perm != "" && !p.Can(perm) {
			http.Error(w, logic.ErrNotPermitted.Error(), http.StatusForbidden)
			return
		}
		back := returnPath(r)
		msg, err := h(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)), p)
		var redirect errRedirect
		switch {
		case errors.As(err, &redirect):
			back = redirect.to
		case errors.Is(err, errRendered):
			return
		case err != nil:
			s.setFlash(w, "error", s.userMessage(r, err))
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		if msg != "" {
			s.setFlash(w, "ok", msg)
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
	})
}

// errRendered means the action already wrote its response (a page showing a
// one-time secret, for example).
var errRendered = errors.New("rendered")

func (s *Server) userMessage(r *http.Request, err error) string {
	if errors.Is(err, logic.ErrInvalid) {
		return strings.TrimPrefix(err.Error(), logic.ErrInvalid.Error()+": ")
	}
	for _, known := range []error{logic.ErrNotPermitted} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	if msg := describeDomain(err); msg != "" {
		return msg
	}
	logx.WithContext(r.Context()).Errorf("console action %s: %v", r.URL.Path, err)
	return "操作失败，请稍后重试；如果持续出现，请查看服务日志"
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	logx.WithContext(r.Context()).Errorf("console %s: %v", r.URL.Path, err)
	http.Error(w, "服务暂时不可用（存储连接失败？），请稍后重试", http.StatusServiceUnavailable)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, v View) {
	v.Nonce = nonceOf(r)
	if v.Me == nil {
		v.Me = principalOf(r)
	}
	if v.Me != nil {
		v.CSRF = v.Me.Session.CSRF
	}
	if v.Flash == nil {
		v.Flash = s.takeFlash(w, r)
	}
	var b bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&b, name, v); err != nil {
		logx.WithContext(r.Context()).Errorf("render %s: %v", name, err)
		http.Error(w, "页面渲染失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b.Bytes())
}

func (s *Server) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func since(t time.Time, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return itoa(int(d/time.Minute)) + " 分钟前"
	case d < 24*time.Hour:
		return itoa(int(d/time.Hour)) + " 小时前"
	default:
		return itoa(int(d/(24*time.Hour))) + " 天前"
	}
}
