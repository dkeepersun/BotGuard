package botguard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
)

// BG1-HMAC-SHA256 request signing, the counterpart of the gateway's
// internal/service/signature. Like Yidun, the secret key never leaves the
// business server; unlike Yidun's MD5 over concatenated parameters, the HMAC
// covers the exact request body bytes.
const (
	scheme     = "BG1-HMAC-SHA256"
	verifyPath = "/v1/verify"

	headerSecretID  = "X-BG-Secret-Id"
	headerTimestamp = "X-BG-Timestamp"
	headerNonce     = "X-BG-Nonce"
	headerSignature = "X-BG-Signature"
)

func canonical(method, path string, timestamp int64, nonce string, body []byte) string {
	sum := sha256.Sum256(body)
	return scheme + "\n" + method + "\n" + path + "\n" + strconv.FormatInt(timestamp, 10) + "\n" + nonce + "\n" + hex.EncodeToString(sum[:])
}

func sign(secretKey []byte, canonical string) string {
	mac := hmac.New(sha256.New, secretKey)
	mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

// newNonce is a 32-character URL-safe nonce from a CSPRNG.
func newNonce() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("botguard: crypto/rand: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// signHeaders sets the signature headers for one attempt. Every retry needs
// fresh ones: a reused nonce is rejected as a replay.
func signHeaders(h http.Header, secretID string, secretKey, body []byte, timestamp int64, nonce string) {
	h.Set(headerSecretID, secretID)
	h.Set(headerTimestamp, strconv.FormatInt(timestamp, 10))
	h.Set(headerNonce, nonce)
	h.Set(headerSignature, sign(secretKey, canonical(http.MethodPost, verifyPath, timestamp, nonce, body)))
}
