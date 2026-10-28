package response

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func JSON(w http.ResponseWriter, status int, value any) { writeJSON(w, status, value, "no-store") }

func writeJSON(w http.ResponseWriter, status int, value any, cache string) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "response encoding failed", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// OK writes the v1 API success envelope.
func OK(w http.ResponseWriter, data any, requestID string) {
	JSON(w, http.StatusOK, map[string]any{"code": "OK", "data": data, "request_id": requestID})
}

// OKCached is OK for public data browsers may keep for maxAge seconds.
func OKCached(w http.ResponseWriter, data any, requestID string, maxAge int) {
	writeJSON(w, http.StatusOK, map[string]any{"code": "OK", "data": data, "request_id": requestID}, "public, max-age="+strconv.Itoa(maxAge))
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
