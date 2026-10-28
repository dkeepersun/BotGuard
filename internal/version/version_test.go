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

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.5.0", "v0.5.0", 0},
		{"v0.5.0", "v0.6.0", -1},
		{"v0.10.0", "v0.9.9", 1},
		{"v1.0.0", "v0.99.99", 1},
		{"v0.6.0-rc.1", "v0.6.0", -1},
		{"v0.6.0", "v0.6.0-rc.1", 1},
		{"v0.6.0-rc.2", "v0.6.0-rc.10", -1},
		{"v0.6.0-rc.1", "v0.6.0-rc.1.1", -1},
		{"v0.6.0-alpha", "v0.6.0-1", 1},
	}
	for _, c := range cases {
		if got, ok := Compare(c.a, c.b); !ok || got != c.want {
			t.Errorf("Compare(%q, %q) = %d, %v; want %d", c.a, c.b, got, ok, c.want)
		}
	}
	for _, bad := range []string{"", "0.5.0", "v0.5", "v0.5.0 (abc1234)", "v01.2.3", "v0.5.0-", "v0.5.0-rc..1", "开发构建（无版本信息）"} {
		if _, ok := Compare(bad, "v0.5.0"); ok || Valid(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
