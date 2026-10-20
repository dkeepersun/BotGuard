package adminweb

import (
	"github.com/dkeepersun/BotGuard/internal/webui"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreviewAndEmbeddedAssets(t *testing.T) {
	for _, name := range []string{"shell.html", "console.html"} {
		w := httptest.NewRecorder()
		page(name)(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "尚未") {
			t.Fatalf("invalid preview %s: %d", name, w.Code)
		}
	}
	for _, path := range []string{"component/pear/pear.js", "component/layui/layui.js", "component/pear/css/pear.css", "admin/css/admin.css"} {
		w := httptest.NewRecorder()
		webui.StaticHandler().ServeHTTP(w, httptest.NewRequest("GET", "/static/"+path, nil))
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("missing asset %s", path)
		}
	}
}
