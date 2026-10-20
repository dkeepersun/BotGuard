package credential

import (
	"strings"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/domain"
)

func TestGeneratedCredentialsMatchContract(t *testing.T) {
	app, err := NewAppKey()
	if err != nil || !domain.ValidAppKey(app) || !strings.HasPrefix(app, "app_") {
		t.Fatalf("app key %q %v", app, err)
	}
	p, err := NewSecretPair()
	if err != nil || !domain.ValidSecretID(p.SecretID) || !strings.HasPrefix(p.SecretID, "sid_") || len(p.SecretKey) != 43 {
		t.Fatalf("secret pair %+v %v", p, err)
	}
	if other, _ := NewSecretPair(); other.SecretID == p.SecretID || other.SecretKey == p.SecretKey {
		t.Fatal("not random")
	}
}
