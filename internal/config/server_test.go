package config

import (
	"net/http"
	"testing"
	"time"
)

func TestServerLimits(t *testing.T) {
	var s http.Server
	ServerLimits(3000)(&s)
	if s.ReadHeaderTimeout != readHeaderTimeout || s.ReadTimeout != readTimeout || s.IdleTimeout != idleTimeout ||
		s.MaxHeaderBytes != maxHeaderBytes || s.WriteTimeout != 13*time.Second {
		t.Fatalf("limits: %+v", &s)
	}
	var unbounded http.Server
	ServerLimits(0)(&unbounded)
	if unbounded.WriteTimeout != 0 || unbounded.ReadHeaderTimeout != readHeaderTimeout {
		t.Fatalf("no handler timeout: %+v", &unbounded)
	}
}
