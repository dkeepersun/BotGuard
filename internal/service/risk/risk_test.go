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
		// An invented pointer type is held to the mouse rules.
		"bogus pointer": {func(s *Signals, _ *Facts) { s.PointerType, s.PointerMovesBeforeClick = "MOUSE", 0 }, 20, []string{ReasonClickWithoutMove}},
		// The header copy is cleaned before comparison; the signal must be too.
		"ua padded":    {func(s *Signals, _ *Facts) { s.UserAgent = "  " + ua + "\n" }, 0, nil},
		"fast click":   {func(s *Signals, _ *Facts) { s.MsToClick = 120 }, 15, []string{ReasonFastClick}},
		"ua mismatch":  {func(_ *Signals, f *Facts) { f.HeaderUA = "curl/8" }, 30, []string{ReasonUAHeaderMismatch}},
		"failures cap": {func(_ *Signals, f *Facts) { f.RecentFailures = 9 }, 40, []string{ReasonIPRecentFailures}},
		"fallback":     {func(_ *Signals, f *Facts) { f.Fallback = true }, 0, []string{ReasonFallbackChosen}},
		// The widget always reports how long the page was open.
		"no interaction": {func(s *Signals, _ *Facts) { s.MsToClick = 0 }, 20, []string{ReasonNoInteraction}},
		"windows touch": {func(s *Signals, f *Facts) {
			s.PointerType, s.PointerMovesBeforeClick = "touch", 0
			s.UserAgent, f.HeaderUA = "", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/140"
		}, 10, []string{ReasonDesktopTouch}},
		// iPadOS Safari says Macintosh.
		"ipad touch":          {func(s *Signals, _ *Facts) { s.PointerType, s.PointerMovesBeforeClick = "touch", 0 }, 0, nil},
		"prefix failures":     {func(_ *Signals, f *Facts) { f.PrefixFailures = 10 }, 15, []string{ReasonPrefixFailures}},
		"few prefix failures": {func(_ *Signals, f *Facts) { f.PrefixFailures = 9 }, 0, nil},
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

// A widget that started on its own (inline mode) has no click to judge.
func TestAutoStartSkipsClickRules(t *testing.T) {
	s := human()
	s.MsToClick, s.PointerMovesBeforeClick, s.Activation = 120, 0, ActivationAuto
	if a := Assess(s, Facts{HeaderUA: ua}); a.Score != 0 {
		t.Fatalf("auto start: %+v", a)
	}
	s.Activation = ""
	if a := Assess(s, Facts{HeaderUA: ua}); a.Score == 0 {
		t.Fatal("the same signals from a click must score")
	}
	if (*Signals)(nil).AutoStart() {
		t.Fatal("no signals is not an auto start")
	}
}
