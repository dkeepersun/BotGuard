package response

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func JSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "response encoding failed", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// OK writes the v1 API success envelope.
func OK(w http.ResponseWriter, data any, requestID string) {
	JSON(w, http.StatusOK, map[string]any{"code": "OK", "data": data, "request_id": requestID})
}

// Fail writes the v1 API error envelope.
func Fail(w http.ResponseWriter, status int, code, message, requestID string, retryAfter int) {
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	}
	JSON(w, status, map[string]string{"code": code, "message": message, "request_id": requestID})
}

func HTML(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	_, _ = w.Write(body)
}
