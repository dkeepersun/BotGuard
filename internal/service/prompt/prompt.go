// Package prompt carries challenge prompts in the languages the widget
// offers. Generators write every language when they build a puzzle (pooled
// puzzles are made before anyone asks for them); the gateway picks one when
// it issues the challenge.
package prompt

import "strings"

// Languages, as BCP 47 tags.
const (
	ZhCN = "zh-CN" // the default
	ZhTW = "zh-TW"
	En   = "en"
)

// Langs lists the supported languages.
var Langs = []string{ZhCN, ZhTW, En}

// Lang maps a page's language tag to a supported one: Simplified Chinese
// for empty or zh / zh-CN / zh-Hans / zh-SG, Traditional for zh-TW / zh-HK /
// zh-MO / zh-Hant, English for anything else.
func Lang(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	switch {
	case t == "":
		return ZhCN
	case t == "zh" || strings.HasPrefix(t, "zh-cn") || strings.HasPrefix(t, "zh-hans") || strings.HasPrefix(t, "zh-sg"):
		return ZhCN
	case strings.HasPrefix(t, "zh-tw") || strings.HasPrefix(t, "zh-hk") || strings.HasPrefix(t, "zh-mo") || strings.HasPrefix(t, "zh-hant"):
		return ZhTW
	case strings.HasPrefix(t, "zh"):
		return ZhCN
	default:
		return En
	}
}

// Chinese reports whether lang reads Chinese, which text kinds need.
func Chinese(lang string) bool { return lang == ZhCN || lang == ZhTW }

// Text is one prompt in each language.
type Text map[string]string

// In returns the prompt in lang, else in Simplified Chinese.
func (t Text) In(lang string) string {
	if s, ok := t[lang]; ok {
		return s
	}
	return t[ZhCN]
}

// Join lists items the way each language does: 、 for Chinese, ", " for
// English.
func Join(lang string, items []string) string {
	if lang == En {
		return strings.Join(items, ", ")
	}
	return strings.Join(items, "、")
}
