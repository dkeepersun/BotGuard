package version

import "testing"

func TestDescribe(t *testing.T) {
	cases := []struct {
		release string
		info    buildInfo
		want    string
	}{
		{"v0.2.0", buildInfo{rev: "abc1234"}, "v0.2.0 (abc1234)"},
		{"v0.2.0", buildInfo{}, "v0.2.0"},
		{"", buildInfo{rev: "abc1234", dirty: true}, "abc1234（含未提交改动）"},
		{"", buildInfo{module: "v0.2.0"}, "v0.2.0"},
		{"", buildInfo{module: "(devel)"}, "开发构建（无版本信息）"},
	}
	for _, c := range cases {
		if got := describe(c.release, c.info); got != c.want {
			t.Errorf("describe(%q, %+v) = %q, want %q", c.release, c.info, got, c.want)
		}
	}
}
