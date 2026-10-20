// Package sdkassets serves the browser widget from the gateway, the way
// Yidun serves load.min.js from its CDN: integrators can include
// <script src="https://gateway/sdk/v1/load.js"> without npm.
//
// The files are copies of packages/browser/src, refreshed with
// `make sync-sdk`; a test fails if they drift.
package sdkassets

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
)

//go:embed v1/*.js
var files embed.FS

// Files lists the published file names under /sdk/v1/.
func Files() []string {
	entries, _ := fs.ReadDir(files, "v1")
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// Handler serves one file. Module scripts and module workers are fetched in
// CORS mode, so cross-origin pages need Access-Control-Allow-Origin; these
// are public files, so any origin may load them.
func Handler(name string) http.HandlerFunc {
	body, err := files.ReadFile(path.Join("v1", name))
	return func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/javascript; charset=utf-8")
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Cross-Origin-Resource-Policy", "cross-origin")
		h.Set("X-Content-Type-Options", "nosniff")
		// Short cache: v1 changes in place for fixes; bump the path for breaking changes.
		h.Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(body)
	}
}
