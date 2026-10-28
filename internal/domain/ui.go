package domain

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// AppUI is how an app's widgets look and what they say, set in the console
// and fetched by the widget when it loads (GET /v1/ui). Anything the page
// sets itself wins; empty fields leave the widget's defaults.
type AppUI struct {
	Lang   string `json:"lang,omitempty"`   // zh-CN, zh-TW, en or auto
	Theme  string `json:"theme,omitempty"`  // light, dark or auto
	Accent string `json:"accent,omitempty"` // #rrggbb
	Radius *int   `json:"radius,omitempty"` // corner radius in px
	// Texts replace the widget's built-in texts by key (see the widget's
	// MESSAGES). They are in Lang (Simplified Chinese when Lang is empty or
	// auto); widgets showing another language ignore them.
	Texts map[string]string `json:"texts,omitempty"`
}

// UITextKeys are the texts the console lets admins change: the ones a page
// shows most.
var UITextKeys = []string{"idle", "idleSub", "loading", "verified", "sliderTitle", "sliderHint", "clickTitle"}

// UILangs and UIThemes are the accepted values; "" leaves the default.
var (
	UILangs  = []string{"", "zh-CN", "zh-TW", "en", "auto"}
	UIThemes = []string{"", "light", "dark", "auto"}
)

const (
	MaxUIText   = 60
	MaxUIRadius = 24
)

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Validate checks every field; the message is for the console.
func (u AppUI) Validate() error {
	if !contains(UILangs, u.Lang) {
		return fmt.Errorf("语言只能是 zh-CN、zh-TW、en 或 auto")
	}
	if !contains(UIThemes, u.Theme) {
		return fmt.Errorf("主题只能是 light、dark 或 auto")
	}
	if u.Accent != "" && !hexColor.MatchString(u.Accent) {
		return fmt.Errorf("主色须为 #rrggbb 格式，例如 #0969da")
	}
	if u.Radius != nil && (*u.Radius < 0 || *u.Radius > MaxUIRadius) {
		return fmt.Errorf("圆角须在 0–%d 像素之间", MaxUIRadius)
	}
	for k, v := range u.Texts {
		if !contains(UITextKeys, k) {
			return fmt.Errorf("不支持修改的文案：%s", k)
		}
		if utf8.RuneCountInString(v) > MaxUIText {
			return fmt.Errorf("文案不能超过 %d 个字符", MaxUIText)
		}
		// The widget sets textContent, so markup would show as text; there
		// is no reason to store it.
		if strings.ContainsAny(v, "<>") {
			return fmt.Errorf("文案不能包含 < 或 >")
		}
	}
	return nil
}

// Empty reports whether u changes nothing.
func (u AppUI) Empty() bool {
	return u.Lang == "" && u.Theme == "" && u.Accent == "" && u.Radius == nil && len(u.Texts) == 0
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
