// Package update finds the latest BotGuard release for the admin console's
// update notice (docs/admin.md 版本更新). It only reads: installing a release
// is the job of the root-owned updater (botguard self-update), never of the
// console process.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dkeepersun/BotGuard/internal/version"
	"github.com/zeromicro/go-zero/core/logx"
)

// DefaultURL is GitHub's latest-release API for this repository; it never
// returns drafts or pre-releases.
const DefaultURL = "https://api.github.com/repos/dkeepersun/BotGuard/releases/latest"

// Release is a published server release.
type Release struct {
	Tag         string
	URL         string // release page, empty when the source gave none usable
	PublishedAt time.Time
}

// State is what the console shows.
type State struct {
	// Current is the running release, empty for a development build.
	Current string
	// Latest is the last release found; nil until a check succeeds.
	Latest    *Release
	CheckedAt time.Time
	// Err is the last check's failure, cleared by the next success.
	Err string
	// Newer: Latest is a later release than Current.
	Newer bool
}

// ErrTooSoon is returned by Check within a minute of the previous try, so a
// button cannot hammer GitHub (60 unauthenticated requests per hour per IP).
var ErrTooSoon = errors.New("刚刚检查过，请一分钟后再试")

const minGap = time.Minute

// Checker looks up the latest release every Interval. A nil Checker means
// checking is turned off.
type Checker struct {
	URL      string
	Current  string
	Interval time.Duration
	Client   *http.Client
	Now      func() time.Time

	mu      sync.Mutex
	state   State
	lastTry time.Time
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Run checks at once and then every Interval until ctx ends.
func (c *Checker) Run(ctx context.Context) {
	for {
		if err := c.Check(ctx); err != nil && !errors.Is(err, ErrTooSoon) {
			logx.Infof("update check: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.Interval):
		}
	}
}

// State returns the latest result.
func (c *Checker) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	s.Current = c.Current
	if s.Latest != nil {
		n, ok := version.Compare(s.Latest.Tag, c.Current)
		s.Newer = ok && n > 0
	}
	return s
}

// Check fetches the latest release now.
func (c *Checker) Check(ctx context.Context) error {
	c.mu.Lock()
	now := c.now()
	if !c.lastTry.IsZero() && now.Sub(c.lastTry) < minGap {
		c.mu.Unlock()
		return ErrTooSoon
	}
	c.lastTry = now
	c.mu.Unlock()

	rel, err := c.fetch(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.CheckedAt = c.now()
	if err != nil {
		c.state.Err = err.Error()
		return err
	}
	c.state.Latest, c.state.Err = rel, ""
	return nil
}

func (c *Checker) fetch(ctx context.Context) (*Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	ua := "BotGuard-admin"
	if c.Current != "" {
		ua += "/" + c.Current
	}
	req.Header.Set("User-Agent", ua)
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法访问 %s：%w", req.URL.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s 返回 HTTP %d", req.URL.Host, resp.StatusCode)
	}
	var body struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("无法解析版本信息：%w", err)
	}
	// Only final releases are offered: pre-releases never reach the latest
	// endpoint, but a mirror might be configured instead.
	if body.Draft || body.Prerelease || !version.Valid(body.TagName) || strings.Contains(body.TagName, "-") {
		return nil, errors.New("返回的不是正式版本号")
	}
	rel := &Release{Tag: body.TagName, PublishedAt: body.PublishedAt}
	if u, err := url.Parse(body.HTMLURL); err == nil && u.Scheme == "https" && u.Host != "" {
		rel.URL = u.String()
	}
	return rel, nil
}
