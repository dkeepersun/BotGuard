package pow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"math/big"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite shared fixtures")

const fixturePath = "../../../api/fixtures/pow-sha256-target-v1.json"

type fixture struct {
	Name        string   `json:"name"`
	Algorithm   string   `json:"algorithm"`
	ChallengeID string   `json:"challenge_id"`
	Params      Params   `json:"params"`
	Nonces      []string `json:"nonces"`
	Valid       bool     `json:"valid"`
}

func digestHex(challengeID string, i int, nonce string) string {
	d := sha256.Sum256(ProofInput(AlgSHA256Target, challengeID, i, nonce))
	return hex.EncodeToString(d[:])
}

func buildFixtures(t *testing.T) []fixture {
	const id = "AAECAwQFBgcICQoLDA0ODw"
	solved, _ := NewParams(8, 64)
	nonces, err := Solve(AlgSHA256Target, id, solved)
	if err != nil {
		t.Fatal(err)
	}
	broken := append([]string(nil), nonces...)
	broken[7] = "18446744073709551615"

	// target exactly equal to the digest must fail; one above must pass.
	equal := digestHex(id, 0, "0")
	above := new(big.Int)
	above.SetString(equal, 16)
	above.Add(above, big.NewInt(1))
	aboveHex := hex.EncodeToString(above.FillBytes(make([]byte, 32)))

	one, _ := NewParams(1, 1)
	return []fixture{
		{"solved k=8 difficulty=64", AlgSHA256Target, id, solved, nonces, true},
		{"one wrong nonce", AlgSHA256Target, id, solved, broken, Verify(AlgSHA256Target, id, solved, broken) == nil},
		{"digest equals target", AlgSHA256Target, id, Params{1, equal}, []string{"0"}, false},
		{"digest one below target", AlgSHA256Target, id, Params{1, aboveHex}, []string{"0"}, true},
		{"k=1 difficulty=1 nonce=0", AlgSHA256Target, id, one, []string{"0"}, true},
		{"20 digit nonce", AlgSHA256Target, id, one, []string{"18446744073709551615"}, true},
		{"leading zero nonce", AlgSHA256Target, id, one, []string{"01"}, false},
		{"21 digit nonce", AlgSHA256Target, id, one, []string{"100000000000000000000"}, false},
		{"nonce count mismatch", AlgSHA256Target, id, solved, nonces[:7], false},
	}
}

func TestSharedFixtures(t *testing.T) {
	if *update {
		b, _ := json.MarshalIndent(buildFixtures(t), "", "  ")
		if err := os.WriteFile(fixturePath, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var cases []fixture
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no fixtures")
	}
	for _, c := range cases {
		got := Verify(c.Algorithm, c.ChallengeID, c.Params, c.Nonces) == nil
		if got != c.Valid {
			t.Errorf("%s: valid=%v, want %v", c.Name, got, c.Valid)
		}
	}
}

func TestTarget(t *testing.T) {
	got := Target(100000)
	if hex.EncodeToString(got[:8]) != "0000a7c5ac471b47" {
		t.Fatalf("target prefix %x", got[:8])
	}
	max := Target(1)
	for _, b := range max {
		if b != 0xff {
			t.Fatal("difficulty 1 must cap at 2^256-1")
		}
	}
}

func TestParamsBounds(t *testing.T) {
	for _, c := range []struct {
		k int
		d uint64
	}{{0, 10}, {MaxPuzzles + 1, 10}, {4, 0}} {
		if _, err := NewParams(c.k, c.d); err == nil {
			t.Fatalf("accepted k=%d d=%d", c.k, c.d)
		}
	}
	if err := Verify("argon2id-target-v1", "x", Params{1, targetHex(1)}, []string{"0"}); err != ErrUnsupportedAlgorithm {
		t.Fatalf("unimplemented algorithm: %v", err)
	}
	if err := Verify(AlgSHA256Target, "x", Params{1, "zz"}, []string{"0"}); err != ErrBadParams {
		t.Fatalf("bad target: %v", err)
	}
}

func targetHex(d uint64) string {
	t := Target(d)
	return hex.EncodeToString(t[:])
}
