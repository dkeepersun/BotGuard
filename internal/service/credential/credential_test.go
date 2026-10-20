package credential

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/domain"
)

func TestGeneratedCredentialsMatchContract(t *testing.T) {
	site, err := NewSiteKey()
	if err != nil || !domain.ValidSiteKey(site) || !strings.HasPrefix(site, "pk_") {
		t.Fatalf("site key %q %v", site, err)
	}
	k, err := NewAPIKey()
	if err != nil || !domain.ValidKeyID(k.KeyID) || len(k.Secret) != 43 || strings.Contains(k.Secret, ".") {
		t.Fatalf("api key %+v %v", k, err)
	}
	if sum := sha256.Sum256([]byte(k.Secret)); string(sum[:]) != string(k.SecretHash) {
		t.Fatal("hash does not match secret")
	}
	if other, _ := NewAPIKey(); other.KeyID == k.KeyID || other.Secret == k.Secret {
		t.Fatal("not random")
	}
}
