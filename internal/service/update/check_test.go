package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fakeGitHub(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "BotGuard-admin") {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckFindsNewerRelease(t *testing.T) {
	srv := fakeGitHub(t, 200, `{"tag_name":"v0.6.0","html_url":"https://github.com/dkeepersun/BotGuard/releases/tag/v0.6.0","published_at":"2026-10-27T08:00:00Z","draft":false,"prerelease":false}`)
	c := &Checker{URL: srv.URL, Current: "v0.5.0", Interval: time.Hour}
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := c.State()
	if s.Latest == nil || s.Latest.Tag != "v0.6.0" || !s.Newer || s.Err != "" || s.CheckedAt.IsZero() {
		t.Fatalf("state %+v", s)
	}
	if s.Latest.URL != "https://github.com/dkeepersun/BotGuard/releases/tag/v0.6.0" || s.Latest.PublishedAt.IsZero() {
		t.Fatalf("release %+v", s.Latest)
	}
}

func TestCheckNotNewer(t *testing.T) {
	srv := fakeGitHub(t, 200, `{"tag_name":"v0.5.0","html_url":"javascript:alert(1)"}`)
	for _, current := range []string{"v0.5.0", "v0.6.0", ""} {
		c := &Checker{URL: srv.URL, Current: current}
		if err := c.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
		if s := c.State(); s.Newer || s.Latest.URL != "" {
			t.Fatalf("current %q: %+v", current, s)
		}
	}
}

func TestCheckRejectsBadAnswers(t *testing.T) {
	for _, body := range []string{
		`{"tag_name":"v0.6.0-rc.1"}`,
		`{"tag_name":"v0.6.0","prerelease":true}`,
		`{"tag_name":"v0.6.0","draft":true}`,
		`{"tag_name":"<script>"}`,
		`not json`,
	} {
		c := &Checker{URL: fakeGitHub(t, 200, body).URL, Current: "v0.5.0"}
		if err := c.Check(context.Background()); err == nil {
			t.Fatalf("%s accepted", body)
		}
		if s := c.State(); s.Latest != nil || s.Err == "" || strings.Contains(s.Err, "<script>") {
			t.Fatalf("%s: %+v", body, s)
		}
	}
	c := &Checker{URL: fakeGitHub(t, 403, `{"message":"rate limited"}`).URL}
	if err := c.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("403: %v", err)
	}
}

// A failed check keeps the last good answer and reports the error.
func TestCheckKeepsLastRelease(t *testing.T) {
	ok := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ok {
			w.WriteHeader(502)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v0.6.0"}`))
	}))
	t.Cleanup(srv.Close)
	now := time.Unix(1_800_000_000, 0)
	c := &Checker{URL: srv.URL, Current: "v0.5.0", Now: func() time.Time { return now }}
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	ok = false
	if err := c.Check(context.Background()); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("second check within a minute: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if err := c.Check(context.Background()); err == nil {
		t.Fatal("502 accepted")
	}
	if s := c.State(); s.Latest == nil || s.Latest.Tag != "v0.6.0" || !s.Newer || !strings.Contains(s.Err, "502") {
		t.Fatalf("state %+v", s)
	}
}
