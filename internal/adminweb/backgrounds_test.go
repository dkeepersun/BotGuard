package adminweb

import (
	"bytes"
	"context"
	"image/png"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
)

// owner signs in a new owner and returns the browser.
func (e *env) owner() *browser {
	e.t.Helper()
	ctx := repository.WithActor(context.Background(), "cli:test", "")
	pw, err := e.console.CreateAdmin(ctx, nil, "", "root", "根", logic.RoleOwner)
	if err != nil {
		e.t.Fatal(err)
	}
	b := e.browser()
	b.login("root", pw)
	token := b.csrf("/profile?force=1")
	b.post("/profile/password", url.Values{"_csrf": {token}, "current": {pw}, "new": {"Strong-Pass-2026"}, "confirm": {"Strong-Pass-2026"}})
	b.login("root", "Strong-Pass-2026")
	return b
}

// upload posts files as the backgrounds form would.
func (b *browser) upload(path, csrf string, files map[string][]byte) *http.Response {
	b.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("_csrf", csrf)
	for name, data := range files {
		w, err := mw.CreateFormFile("files", name)
		if err != nil {
			b.t.Fatal(err)
		}
		_, _ = w.Write(data)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", b.base+path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", b.base)
	req.Header.Set("Referer", b.base+b.referer)
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func testPNG(t *testing.T, seed uint64, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, imagegen.Background(rand.New(rand.NewPCG(seed, 1)), w, h)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestBackgrounds(t *testing.T) {
	e := newEnv(t)
	b := e.owner()
	key, err := e.web.Apps.CreateApp(repository.WithActor(context.Background(), "cli:test", ""), "商城", "")
	if err != nil {
		t.Fatal(err)
	}
	page := "/apps/" + key + "?tab=backgrounds"
	if resp, body := b.get(page); resp.StatusCode != 200 || !strings.Contains(body, "背景来源") || !strings.Contains(body, "还没有上传照片") {
		t.Fatalf("backgrounds tab: %d", resp.StatusCode)
	}

	// Two good pictures and one too small: the good ones are stored.
	token := b.csrf(page)
	resp := b.upload("/apps/"+key+"/backgrounds/upload", token, map[string][]byte{
		"a.png": testPNG(t, 1, 800, 500), "b.png": testPNG(t, 2, 1000, 700), "tiny.png": testPNG(t, 3, 320, 200),
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("upload: %d", resp.StatusCode)
	}
	d, err := e.apps.AppDetail(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Backgrounds) != 2 || d.Background.Mode != domain.BackgroundMixed || !d.Background.Builtin {
		t.Fatalf("after upload: %+v %+v", d.Backgrounds, d.Background)
	}
	_, body := b.get(page)
	if !strings.Contains(body, "tiny.png") || !strings.Contains(body, "640×400") {
		t.Fatalf("no message about the small picture: %.2000s", body)
	}
	// The same picture again is refused.
	b.upload("/apps/"+key+"/backgrounds/upload", b.csrf(page), map[string][]byte{"again.png": testPNG(t, 1, 800, 500)})
	if d, _ := e.apps.AppDetail(context.Background(), key); len(d.Backgrounds) != 2 {
		t.Fatalf("duplicate stored: %d", len(d.Backgrounds))
	}

	// The stored photo is served as a JPEG to signed-in admins only.
	img := "/apps/" + key + "/backgrounds/" + strconv.FormatInt(d.Backgrounds[0].ID, 10)
	if resp, body := b.get(img); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" || !strings.HasPrefix(body, "\xff\xd8") {
		t.Fatalf("image: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp, _ := e.browser().get(img); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("signed-out image: %d", resp.StatusCode)
	}

	// The gateway sees the setting and the uploads.
	gw := repository.NewSQLApps(e.db, nil)
	app, err := gw.AppByKey(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if app.Background.Uploads != 2 || app.Background.Version != 2 || app.Background.Default() {
		t.Fatalf("gateway view: %+v", app.Background)
	}
	if jpegs, err := gw.AppPhotos(context.Background(), app.ID); err != nil || len(jpegs) != 2 {
		t.Fatalf("app photos: %d %v", len(jpegs), err)
	}

	// Mode and built-in photos, then delete one upload.
	b.post("/apps/"+key+"/backgrounds/mode", url.Values{"_csrf": {b.csrf(page)}, "mode": {"photo"}})
	b.post("/apps/"+key+"/backgrounds/delete", url.Values{"_csrf": {b.csrf(page)}, "id": {strconv.FormatInt(d.Backgrounds[0].ID, 10)}})
	app, _ = gw.AppByKey(context.Background(), key)
	if app.Background.Mode != domain.BackgroundPhoto || !app.Background.NoBuiltin || app.Background.Uploads != 1 || app.Background.Version != 3 {
		t.Fatalf("after changes: %+v", app.Background)
	}
	if resp, body := b.post("/apps/"+key+"/backgrounds/mode", url.Values{"_csrf": {b.csrf(page)}, "mode": {"bogus"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("bad mode: %d %s", resp.StatusCode, body)
	}
	if app, _ = gw.AppByKey(context.Background(), key); app.Background.Mode != domain.BackgroundPhoto {
		t.Fatalf("bad mode stored: %+v", app.Background)
	}

	// Every change is audited.
	_, body = b.get("/audit?op=background.")
	for _, op := range []string{"上传背景图", "修改背景来源", "删除背景图"} {
		if !strings.Contains(body, op) {
			t.Fatalf("audit lacks %s", op)
		}
	}
	// Photo previews render.
	if resp, body := b.get("/api/preview?kind=slider&bg=photo"); resp.StatusCode != 200 || !strings.Contains(body, "data:image/jpeg") {
		t.Fatalf("photo preview: %d", resp.StatusCode)
	}
}
