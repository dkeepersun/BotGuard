package webui

import (
	"embed"
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
