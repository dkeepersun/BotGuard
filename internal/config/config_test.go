package config

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/zeromicro/go-zero/core/conf"
)

func TestAdminConfigValidation(t *testing.T) {
	base := func(host string, secure bool) AdminConfig {
		var c AdminConfig
		c.Host, c.CookieSecure = host, secure
		c.Redis.Host = "127.0.0.1:6379"
		c.MySQL.DataSource = "u:p@tcp(127.0.0.1:3306)/botguard"
		c.Session.IdleMinutes, c.Session.AbsoluteHours = 30, 8
		return c
	}
	if err := base("0.0.0.0", true).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := base("127.0.0.1", false).Validate(); err != nil {
		t.Fatal(err)
	}
	if base("0.0.0.0", false).Validate() == nil {
		t.Fatal("insecure cookies on a public listener accepted")
	}
	t.Setenv("BOTGUARD_ADMIN_INSECURE_COOKIE", "1")
	if err := base("0.0.0.0", false).Validate(); err != nil {
		t.Fatal(err)
	}
	c := base("127.0.0.1", true)
	c.SecretKEK = "k1:not-base64"
	if c.Validate() == nil {
		t.Fatal("bad KEK accepted")
	}
}

// The shipped configuration must validate, and MaxDifficulty 0 must not:
// it would turn every challenge into a 503 blamed on the app policy.
func TestShippedGatewayConfigValidates(t *testing.T) {
	t.Setenv("BOTGUARD_MYSQL_DSN", "u:p@tcp(127.0.0.1:3306)/botguard")
	t.Setenv("BOTGUARD_CHALLENGE_KEY_K1", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("BOTGUARD_SECRET_KEK", "k1:"+base64.StdEncoding.EncodeToString(make([]byte, 32)))
	var c GatewayConfig
	if err := conf.Load("../../configs/gateway.yaml", &c, conf.UseEnv()); err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(c.SecretKEKPrevious) != 1 || c.SecretKEKPrevious[0] != "" {
		t.Fatalf("unset previous KEK: %q", c.SecretKEKPrevious)
	}
	t.Setenv("BOTGUARD_SECRET_KEK_PREVIOUS", "k0:"+base64.StdEncoding.EncodeToString(make([]byte, 32)))
	var rotating GatewayConfig
	if err := conf.Load("../../configs/gateway.yaml", &rotating, conf.UseEnv()); err != nil {
		t.Fatal(err)
	}
	if err := rotating.Validate(); err != nil || rotating.SecretKEKPrevious[0] == "" {
		t.Fatalf("previous KEK from env: %v %q", err, rotating.SecretKEKPrevious)
	}
	c.Challenge.MaxDifficulty = 0
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "MaxDifficulty") {
		t.Fatalf("MaxDifficulty 0: %v", err)
	}
}
