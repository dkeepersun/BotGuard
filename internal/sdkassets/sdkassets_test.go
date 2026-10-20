package sdkassets

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The embedded copies must match the browser package byte for byte.
func TestInSyncWithBrowserPackage(t *testing.T) {
	src, err := filepath.Glob("../../packages/browser/src/*.js")
	if err != nil || len(src) == 0 {
		t.Fatalf("browser sources: %v", err)
	}
	var names []string
	for _, p := range src {
		name := filepath.Base(p)
		names = append(names, name)
		want, _ := os.ReadFile(p)
		got, err := files.ReadFile("v1/" + name)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s differs from packages/browser/src: run `make sync-sdk`", name)
		}
	}
	if !slices.Equal(names, Files()) {
		t.Errorf("file sets differ: %v vs %v", names, Files())
	}
}

func TestHandlerHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	Handler("load.js")(w, httptest.NewRequest("GET", "/sdk/v1/load.js", nil))
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "*" || w.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	w = httptest.NewRecorder()
	Handler("nope.js")(w, httptest.NewRequest("GET", "/sdk/v1/nope.js", nil))
	if w.Code != 404 {
		t.Fatalf("missing file: %d", w.Code)
	}
}
