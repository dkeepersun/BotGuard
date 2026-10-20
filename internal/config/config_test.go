package config

import (
	"github.com/zeromicro/go-zero/rest"
	"testing"
)

func TestBootstrapRejectsPublicBind(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "::", "192.168.1.2", "localhost", ""} {
		if (AdminConfig{rest.RestConf{Host: host}}).ValidateBootstrap() == nil {
			t.Fatalf("accepted %q", host)
		}
	}
	for _, host := range []string{"127.0.0.1", "::1"} {
		if err := (AdminConfig{rest.RestConf{Host: host}}).ValidateBootstrap(); err != nil {
			t.Fatal(err)
		}
	}
}
