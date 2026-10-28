package prompt

import "testing"

func TestLang(t *testing.T) {
	for in, want := range map[string]string{
		"": ZhCN, "zh": ZhCN, "zh-CN": ZhCN, "zh-Hans-CN": ZhCN, "zh-SG": ZhCN,
		"zh-TW": ZhTW, "zh-hk": ZhTW, "zh-Hant": ZhTW, "zh-MO": ZhTW,
		"en": En, "en-US": En, "ja": En, "fr-FR": En,
	} {
		if got := Lang(in); got != want {
			t.Errorf("Lang(%q) = %q, want %q", in, got, want)
		}
	}
	if (Text{ZhCN: "a", En: "b"}).In(ZhTW) != "a" {
		t.Error("missing language must fall back to Simplified Chinese")
	}
}
