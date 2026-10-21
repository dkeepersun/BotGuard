package webui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
)

//go:embed static
var assets embed.FS

func StaticHandler() http.Handler {
	root, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(root)))
}

func Paths() []string {
	var paths []string
	err := fs.WalkDir(assets, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, path[len("static/"):])
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	return paths
}

// Version returns a short content hash of a static file, for cache-busting
// query strings; "" when the file does not exist.
func Version(path string) string {
	b, err := assets.ReadFile("static/" + path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:4])
}
