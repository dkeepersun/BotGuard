package risk

import "strings"

// Device names the platform and browser of a user agent in a few words
// ("iPhone · Safari", "Android · 微信"), for the console's event list. It is
// deliberately coarse: no versions, nothing identifying.
func Device(ua string) string {
	if ua == "" {
		return ""
	}
	has := func(s string) bool { return strings.Contains(ua, s) }
	var platform string
	switch {
	case has("iPhone"):
		platform = "iPhone"
	case has("iPad"):
		platform = "iPad"
	case has("HarmonyOS") || has("OpenHarmony"):
		platform = "鸿蒙"
	case has("Android"):
		platform = "Android"
	case has("Windows"):
		platform = "Windows"
	case has("Macintosh"):
		platform = "Mac"
	case has("CrOS"):
		platform = "ChromeOS"
	case has("Linux"):
		platform = "Linux"
	default:
		platform = "其他"
	}
	var browser string
	switch {
	case has("HeadlessChrome"):
		browser = "无头 Chrome"
	case has("MicroMessenger"):
		browser = "微信"
	case has("AlipayClient"):
		browser = "支付宝"
	case has("DingTalk"):
		browser = "钉钉"
	case has("MQQBrowser") || has(" QQ/"):
		browser = "QQ"
	case has("UCBrowser"):
		browser = "UC"
	case has("Edg/") || has("EdgA/") || has("EdgiOS/"):
		browser = "Edge"
	case has("SamsungBrowser"):
		browser = "三星浏览器"
	case has("Firefox/") || has("FxiOS/"):
		browser = "Firefox"
	case has("CriOS/") || has("Chrome/"):
		browser = "Chrome"
	case has("Safari/"):
		browser = "Safari"
	default:
		browser = "其他浏览器"
	}
	return platform + " · " + browser
}
