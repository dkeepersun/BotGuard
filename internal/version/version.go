// Package version reports which build of BotGuard is running.
package version

import (
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
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

// Compare orders two release versions such as v0.5.0 and v0.6.0-rc.1 by
// semantic versioning: -1 when a is older, 0 when equal, 1 when newer. A
// pre-release sorts before its release. ok is false when either is not a
// vX.Y.Z[-pre] string (a development build has no release version).
func Compare(a, b string) (n int, ok bool) {
	va, okA := parse(a)
	vb, okB := parse(b)
	if !okA || !okB {
		return 0, false
	}
	for i := range 3 {
		if va.core[i] != vb.core[i] {
			return cmp(va.core[i], vb.core[i]), true
		}
	}
	switch {
	case va.pre == "" && vb.pre == "":
		return 0, true
	case va.pre == "":
		return 1, true
	case vb.pre == "":
		return -1, true
	}
	pa, pb := strings.Split(va.pre, "."), strings.Split(vb.pre, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if c := comparePre(pa[i], pb[i]); c != 0 {
			return c, true
		}
	}
	return cmp(len(pa), len(pb)), true
}

// Valid reports whether v is a release version Compare understands.
func Valid(v string) bool { _, ok := parse(v); return ok }

type semver struct {
	core [3]int
	pre  string
}

var semverPattern = regexp.MustCompile(`^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?$`)

func parse(v string) (semver, bool) {
	m := semverPattern.FindStringSubmatch(v)
	if m == nil {
		return semver{}, false
	}
	var s semver
	for i := range 3 {
		s.core[i], _ = strconv.Atoi(m[i+1])
	}
	s.pre = m[4]
	return s, true
}

// comparePre orders pre-release identifiers: numbers numerically and before
// words, words by ASCII.
func comparePre(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		return cmp(na, nb)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func cmp(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
