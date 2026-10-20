package risk

import (
	"reflect"
	"testing"
)

const ua = "Mozilla/5.0 (Macintosh) Chrome/140"

func human() *Signals {
	return &Signals{UserAgent: ua, MsToClick: 2400, PointerMovesBeforeClick: 35, PointerType: "mouse"}
}

func TestCleanBrowserScoresZero(t *testing.T) {
	if a := Assess(human(), Facts{HeaderUA: ua}); a.Score != 0 || len(a.Reasons) != 0 {
		t.Fatalf("%+v", a)
	}
	// A touch tap or keyboard activation without prior pointer moves is normal.
	for _, pt := range []string{"touch", "keyboard", "pen"} {
		s := human()
		s.PointerType, s.PointerMovesBeforeClick = pt, 0
		if a := Assess(s, Facts{HeaderUA: ua}); a.Score != 0 {
			t.Errorf("%s: %+v", pt, a)
		}
	}
}

func TestRulesAndCaps(t *testing.T) {
	cases := map[string]struct {
		mut   func(*Signals, *Facts)
		score int
		want  []string
	}{
		"webdriver":     {func(s *Signals, _ *Facts) { s.Webdriver = true }, 60, []string{ReasonWebdriver}},
		"mouse no move": {func(s *Signals, _ *Facts) { s.PointerMovesBeforeClick = 0 }, 20, []string{ReasonClickWithoutMove}},
		"fast click":    {func(s *Signals, _ *Facts) { s.MsToClick = 120 }, 15, []string{ReasonFastClick}},
		"ua mismatch":   {func(_ *Signals, f *Facts) { f.HeaderUA = "curl/8" }, 30, []string{ReasonUAHeaderMismatch}},
		"failures cap":  {func(_ *Signals, f *Facts) { f.RecentFailures = 9 }, 30, []string{ReasonIPRecentFailures}},
		"fallback":      {func(_ *Signals, f *Facts) { f.Fallback = true }, 0, []string{ReasonFallbackChosen}},
		"headless stack": {func(s *Signals, _ *Facts) {
			s.Webdriver, s.HeadlessUA, s.SoftwareGL = true, true, true
		}, 100, []string{ReasonHeadlessUA, ReasonSoftwareGL, ReasonWebdriver}},
	}
	for name, c := range cases {
		s, f := human(), Facts{HeaderUA: ua}
		c.mut(s, &f)
		a := Assess(s, f)
		if a.Score != c.score || !reflect.DeepEqual(a.Reasons, c.want) {
			t.Errorf("%s: %+v", name, a)
		}
	}
	if a := Assess(nil, Facts{}); a.Score != 40 || !reflect.DeepEqual(a.Reasons, []string{ReasonNoSignals}) {
		t.Fatalf("no signals: %+v", a)
	}
}

func TestAddAndLevel(t *testing.T) {
	a := Assessment{Score: 70, Reasons: []string{"A", "B"}}.Add(Assessment{Score: 50, Reasons: []string{"B", "C"}})
	if a.Score != 100 || !reflect.DeepEqual(a.Reasons, []string{"A", "B", "C"}) {
		t.Fatalf("%+v", a)
	}
	for score, want := range map[int]string{0: LevelLow, 29: LevelLow, 30: LevelMedium, 69: LevelMedium, 70: LevelHigh} {
		if got := Level(score, 30, 70); got != want {
			t.Errorf("%d: %s", score, got)
		}
	}
}
