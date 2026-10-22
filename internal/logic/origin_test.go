package logic

import (
	"context"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
)

func (h *harness) tokenFrom(t *testing.T, origin string) (string, error) {
	t.Helper()
	ctx := context.Background()
	c, _, err := h.g.Issue(ctx, IssueInput{AppKey: appA, Action: "login", Origin: origin, ClientIP: h.ip})
	if err != nil {
		return "", err
	}
	nonces, _ := pow.Solve(c.Algorithm, c.ChallengeID, c.Params)
	out, _, err := h.g.Solve(ctx, SolveInput{AppKey: appA, Challenge: c.Challenge, Nonces: nonces, Origin: origin, ClientIP: h.ip})
	if err != nil {
		return "", err
	}
	return out.Token, nil
}

// Wildcard and any-origin patterns work through issue, solve and verify, and
// verify reports the page origin so the business server can check it.
func TestOriginPatterns(t *testing.T) {
	h := newHarness(t)
	app := h.apps.apps[appA]
	app.Origins = []string{"https://*.shop.example"}

	token, err := h.tokenFrom(t, "https://m.shop.example")
	if err != nil {
		t.Fatalf("subdomain: %v", err)
	}
	v, err := h.verify(token, "login", "r-sub")
	if err != nil || !v.Success || v.Origin != "https://m.shop.example" {
		t.Fatalf("verify subdomain: %+v %v", v, err)
	}
	for _, o := range []string{"https://shop.example", "http://m.shop.example", "https://m.shop.example.evil"} {
		if _, err := h.tokenFrom(t, o); code(err) != "ORIGIN_NOT_ALLOWED" {
			t.Errorf("%s: %v", o, err)
		}
	}

	app.Origins = []string{domain.AnyOrigin}
	token, err = h.tokenFrom(t, "https://brand-x.test")
	if err != nil {
		t.Fatalf("any: %v", err)
	}
	// Narrowing the patterns after issue still revokes the token at verify.
	app.Origins = []string{"https://*.shop.example"}
	if v, err := h.verify(token, "login", "r-any"); err != nil || v.Success || v.Reason != "ORIGIN_NOT_ALLOWED" {
		t.Fatalf("verify after narrowing: %+v %v", v, err)
	}
}
