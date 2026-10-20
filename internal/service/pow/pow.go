// Package pow implements the multi-puzzle, numeric-target proof of work
// described in api/contract-v1.md.
package pow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"strconv"
)

// AlgSHA256Target is the only algorithm implemented in P1.
const AlgSHA256Target = "sha256-target-v1"

const (
	MaxPuzzles     = 32
	MaxNonceDigits = 20
)

var (
	ErrUnsupportedAlgorithm = errors.New("pow: unsupported algorithm")
	ErrBadParams            = errors.New("pow: invalid params")
	ErrBadNonces            = errors.New("pow: malformed nonces")
	ErrProofInvalid         = errors.New("pow: proof does not meet target")
)

// Params are the server-chosen puzzle parameters carried inside a signed challenge.
type Params struct {
	Puzzles int    `json:"puzzles"`
	Target  string `json:"target"`
}

// NewParams builds params for k puzzles, each needing difficulty expected attempts.
func NewParams(puzzles int, difficulty uint64) (Params, error) {
	if puzzles < 1 || puzzles > MaxPuzzles || difficulty < 1 {
		return Params{}, ErrBadParams
	}
	t := Target(difficulty)
	return Params{Puzzles: puzzles, Target: hex.EncodeToString(t[:])}, nil
}

// Target returns floor(2^256 / difficulty), capped at 2^256-1.
func Target(difficulty uint64) [32]byte {
	var out [32]byte
	if difficulty <= 1 {
		for i := range out {
			out[i] = 0xff
		}
		return out
	}
	n := new(big.Int).Lsh(big.NewInt(1), 256)
	n.Quo(n, new(big.Int).SetUint64(difficulty))
	n.FillBytes(out[:])
	return out
}

// ProofInput is the exact byte string hashed for puzzle i.
func ProofInput(algorithm, challengeID string, i int, nonce string) []byte {
	return []byte("BG1\n" + algorithm + "\n" + challengeID + "\n" + strconv.Itoa(i) + "\n" + nonce)
}

// ValidNonce enforces the canonical decimal form: [0-9]{1,20}, no leading zero.
func ValidNonce(s string) bool {
	if len(s) == 0 || len(s) > MaxNonceDigits {
		return false
	}
	if len(s) > 1 && s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// CheckNonces validates count and format without hashing, so callers can
// reject malformed submissions before spending any state.
func CheckNonces(p Params, nonces []string) error {
	if len(nonces) != p.Puzzles {
		return ErrBadNonces
	}
	for _, n := range nonces {
		if !ValidNonce(n) {
			return ErrBadNonces
		}
	}
	return nil
}

func decodeTarget(p Params) ([32]byte, error) {
	var t [32]byte
	if p.Puzzles < 1 || p.Puzzles > MaxPuzzles || len(p.Target) != 64 {
		return t, ErrBadParams
	}
	b, err := hex.DecodeString(p.Target)
	if err != nil {
		return t, ErrBadParams
	}
	copy(t[:], b)
	return t, nil
}

// Verify checks every puzzle and stops at the first failure.
func Verify(algorithm, challengeID string, p Params, nonces []string) error {
	if algorithm != AlgSHA256Target {
		return ErrUnsupportedAlgorithm
	}
	target, err := decodeTarget(p)
	if err != nil {
		return err
	}
	if err := CheckNonces(p, nonces); err != nil {
		return err
	}
	for i, n := range nonces {
		d := sha256.Sum256(ProofInput(algorithm, challengeID, i, n))
		if bytes.Compare(d[:], target[:]) >= 0 {
			return ErrProofInvalid
		}
	}
	return nil
}

// Solve finds the smallest valid nonce for each puzzle. It exists for tests,
// fixtures and reference clients; the server never calls it.
func Solve(algorithm, challengeID string, p Params) ([]string, error) {
	if algorithm != AlgSHA256Target {
		return nil, ErrUnsupportedAlgorithm
	}
	target, err := decodeTarget(p)
	if err != nil {
		return nil, err
	}
	nonces := make([]string, p.Puzzles)
	for i := range nonces {
		for n := uint64(0); ; n++ {
			s := strconv.FormatUint(n, 10)
			d := sha256.Sum256(ProofInput(algorithm, challengeID, i, s))
			if bytes.Compare(d[:], target[:]) < 0 {
				nonces[i] = s
				break
			}
		}
	}
	return nonces, nil
}
