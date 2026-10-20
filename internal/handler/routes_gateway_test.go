package handler

import (
	"net/http/httptest"
	"testing"
)

func TestBootstrapCannotReportReady(t *testing.T) {
	w := httptest.NewRecorder()
	Readiness(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != 503 {
		t.Fatalf("unimplemented verification reported %d", w.Code)
	}
}
