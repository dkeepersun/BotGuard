// Package adminweb is a read-only, loopback-only PearAdmin preview.
package adminweb

import (
	"bytes"
	"embed"
	"github.com/dkeepersun/BotGuard/internal/handler"
	"github.com/dkeepersun/BotGuard/internal/response"
	"github.com/dkeepersun/BotGuard/internal/webui"
	"github.com/zeromicro/go-zero/rest"
	"html/template"
	"net/http"
)

//go:embed templates
var files embed.FS
var pages = template.Must(template.ParseFS(files, "templates/*.html"))

func page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		if err := pages.ExecuteTemplate(&b, name, nil); err != nil {
			http.Error(w, "页面渲染失败", 500)
			return
		}
		response.HTML(w, b.Bytes())
	}
}

func RegisterHandlers(s *rest.Server) {
	static := webui.StaticHandler()
	s.AddRoutes([]rest.Route{
		{Method: "GET", Path: "/", Handler: page("shell.html")},
		{Method: "GET", Path: "/console", Handler: page("console.html")},
		{Method: "GET", Path: "/pear-config", Handler: pearConfig},
		{Method: "GET", Path: "/menus", Handler: menus},
		{Method: "GET", Path: "/healthz", Handler: handler.Health},
		{Method: "GET", Path: "/readyz", Handler: handler.Readiness},
	})
	// Explicit embedded file routes avoid catch-all router ambiguity.
	for _, path := range staticPaths() {
		s.AddRoute(rest.Route{Method: "GET", Path: "/static/" + path, Handler: static.ServeHTTP})
	}
}

func pearConfig(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, 200, map[string]any{
		"logo":   map[string]any{"title": "BotGuard", "image": "/static/admin/logo.svg"},
		"menu":   map[string]any{"data": "/menus", "method": "GET", "accordion": true, "collapse": false, "control": false, "select": "console", "async": true},
		"tab":    map[string]any{"enable": true, "keepState": true, "session": false, "preload": false, "max": "10", "index": map[string]string{"id": "console", "href": "/console", "title": "首页"}},
		"theme":  map[string]any{"defaultColor": "1", "defaultMenu": "dark-theme", "defaultHeader": "light-theme", "allowCustom": false, "banner": false},
		"colors": []map[string]string{{"id": "1", "color": "#16baaa", "second": "#ecf5ff"}},
		"other":  map[string]any{"keepLoad": "600", "autoHead": false, "footer": false},
		"header": map[string]bool{"message": false},
	})
}
func menus(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, 200, []map[string]any{{"id": "console", "title": "首页", "type": 1, "icon": "layui-icon layui-icon-home", "href": "/console", "openType": "iframe"}})
}
