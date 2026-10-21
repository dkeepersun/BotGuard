// Package version reports which build of BotGuard is running.
package version

import (
	"runtime/debug"
)

// Version is the release version, set by scripts/release.sh:
//
//	-ldflags "-X github.com/dkeepersun/BotGuard/internal/version.Version=v0.2.0"
//
// It stays empty for go build / go run in a checkout.
var Version = ""

// String returns the release version with its commit, e.g. "v0.2.0 (abc1234)".
// Builds without a release version fall back to the commit (go build in a
// checkout) or the module version (go install ...@vX.Y.Z).
func String() string {
	return describe(Version, readBuildInfo())
}

type buildInfo struct {
	module string
	rev    string
	dirty  bool
}

func readBuildInfo() buildInfo {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return buildInfo{}
	}
	info := buildInfo{module: bi.Main.Version}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.rev = s.Value
		case "vcs.modified":
			info.dirty = s.Value == "true"
		}
	}
	if len(info.rev) > 7 {
		info.rev = info.rev[:7]
	}
	return info
}

func describe(release string, info buildInfo) string {
	rev := info.rev
	if rev != "" && info.dirty {
		rev += "（含未提交改动）"
	}
	switch {
	case release != "" && rev != "":
		return release + " (" + rev + ")"
	case release != "":
		return release
	case rev != "":
		return rev
	case info.module != "" && info.module != "(devel)":
		return info.module
	default:
		return "开发构建（无版本信息）"
	}
}
