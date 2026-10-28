package adminweb

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/repository"
)

func TestAppUI(t *testing.T) {
	e := newEnv(t)
	b := e.owner()
	key, err := e.web.Apps.CreateApp(repository.WithActor(context.Background(), "cli:test", ""), "商城", "")
	if err != nil {
		t.Fatal(err)
	}
	page := "/apps/" + key + "?tab=ui"
	if resp, body := b.get(page); resp.StatusCode != 200 || !strings.Contains(body, "外观与文案") || !strings.Contains(body, "点击进行人机验证") {
		t.Fatalf("ui tab: %d", resp.StatusCode)
	}
	b.post("/apps/"+key+"/ui", url.Values{"_csrf": {b.csrf(page)}, "lang": {"en"}, "theme": {"auto"}, "accent": {"#ff6a00"}, "radius": {"12"},
		"text_idle": {"  Prove you are human "}, "text_verified": {""}})
	gw := repository.NewSQLApps(e.db, nil)
	app, err := gw.AppByKey(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	ui := app.UI
	if ui.Lang != "en" || ui.Theme != "auto" || ui.Accent != "#ff6a00" || ui.Radius == nil || *ui.Radius != 12 ||
		len(ui.Texts) != 1 || ui.Texts["idle"] != "Prove you are human" {
		t.Fatalf("stored: %+v", ui)
	}
	// Bad input is refused and changes nothing.
	for _, form := range []url.Values{{"accent": {"red"}}, {"radius": {"99"}}, {"text_idle": {"<b>x</b>"}}, {"theme": {"neon"}}} {
		form.Set("_csrf", b.csrf(page))
		b.post("/apps/"+key+"/ui", form)
		if again, _ := gw.AppByKey(context.Background(), key); again.UI.Accent != "#ff6a00" {
			t.Fatalf("%v accepted: %+v", form, again.UI)
		}
	}
	// Saving an empty form clears the setting.
	b.post("/apps/"+key+"/ui", url.Values{"_csrf": {b.csrf(page)}})
	if app, _ = gw.AppByKey(context.Background(), key); !app.UI.Empty() {
		t.Fatalf("not cleared: %+v", app.UI)
	}
	if _, body := b.get("/audit?op=ui."); !strings.Contains(body, "修改外观与文案") {
		t.Fatal("not audited")
	}
}
