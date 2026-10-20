// Package signature implements the BG1-HMAC-SHA256 request signature used by
// business servers, modelled on Yidun's secretId/signature/timestamp/nonce
// scheme but with HMAC-SHA256 over a canonical string instead of MD5 over
// concatenated parameters.
//
//	canonical = "BG1-HMAC-SHA256\n" + METHOD + "\n" + PATH + "\n" +
//	            timestamp + "\n" + nonce + "\n" + hex(sha256(body))
//	signature = hex(HMAC-SHA256(secret_key, canonical))
//
// PATH is the API route ("/v1/verify"), not the URL the client happened to
// call, so a reverse proxy that rewrites prefixes does not break signatures.
package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
)

const (
	Scheme = "BG1-HMAC-SHA256"

	HeaderSecretID  = "X-BG-Secret-Id"
	HeaderTimestamp = "X-BG-Timestamp"
	HeaderNonce     = "X-BG-Nonce"
	HeaderSignature = "X-BG-Signature"
)

var (
	noncePattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
	signaturePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func ValidNonce(s string) bool     { return noncePattern.MatchString(s) }
func ValidSignature(s string) bool { return signaturePattern.MatchString(s) }

func Canonical(method, path string, timestamp int64, nonce string, body []byte) string {
	sum := sha256.Sum256(body)
	return Scheme + "\n" + method + "\n" + path + "\n" + strconv.FormatInt(timestamp, 10) + "\n" + nonce + "\n" + hex.EncodeToString(sum[:])
}

func Sign(secretKey []byte, canonical string) string {
	m := hmac.New(sha256.New, secretKey)
	m.Write([]byte(canonical))
	return hex.EncodeToString(m.Sum(nil))
}

// Equal compares in constant time.
func Equal(secretKey []byte, canonical, got string) bool {
	return hmac.Equal([]byte(Sign(secretKey, canonical)), []byte(got))
}
