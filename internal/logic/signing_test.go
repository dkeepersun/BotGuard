package logic

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/dkeepersun/BotGuard/internal/service/signature"
)

var nonceSeq atomic.Int64

// signed builds a VerifyInput the way an SDK would: JSON body, fresh nonce,
// BG1-HMAC-SHA256 over the exact body bytes.
func signed(secretID, secretKey string, now time.Time, appKey, token, action, requestID string) VerifyInput {
	body, _ := json.Marshal(map[string]string{"app_key": appKey, "token": token, "action": action, "request_id": requestID})
	nonce := fmt.Sprintf("nonce_%016d", nonceSeq.Add(1))
	ts := now.Unix()
	sig := signature.Sign([]byte(secretKey), signature.Canonical("POST", "/v1/verify", ts, nonce, body))
	return VerifyInput{SecretID: secretID, Timestamp: strconv.FormatInt(ts, 10), Nonce: nonce, Signature: sig, Body: body,
		AppKey: appKey, Token: token, Action: action, RequestID: requestID}
}
