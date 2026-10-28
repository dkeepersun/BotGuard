package update

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newUpgrader(t *testing.T, status string) *Upgrader {
	t.Helper()
	data, dir := t.TempDir(), t.TempDir()
	if status != "" {
		if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(status), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return &Upgrader{RequestFile: filepath.Join(data, "update-request"), Dir: dir}
}

func TestUpgraderNotInstalled(t *testing.T) {
	u := newUpgrader(t, "")
	if p, err := u.Progress(); p != nil || err != nil {
		t.Fatalf("progress %+v %v", p, err)
	}
	if err := u.Request("v0.6.0", "root"); err == nil {
		t.Fatal("request accepted without an updater")
	}
}

func TestUpgraderRequest(t *testing.T) {
	u := newUpgrader(t, `{"state":"ready","method":"package"}`)
	for _, bad := range []string{"v0.6.0-rc.1", "0.6.0", "v0.6.0\nversion=v0.1.0", "latest"} {
		if err := u.Request(bad, "root"); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if err := u.Request("v0.6.0", "Root\nversion=v0.1.0"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(u.RequestFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || lines[0] != "version=v0.6.0" || lines[1] != "requested_by=unknown" || !strings.HasPrefix(lines[2], "requested_at=") {
		t.Fatalf("request file %q", b)
	}
	// Nothing but the request is left in the directory.
	if entries, _ := os.ReadDir(filepath.Dir(u.RequestFile)); len(entries) != 1 {
		t.Fatalf("stray files: %v", entries)
	}
	p, err := u.Progress()
	if err != nil || p == nil || !p.Pending || p.Method != "package" || !u.Busy(p) {
		t.Fatalf("progress %+v %v", p, err)
	}
	if err := u.Request("v0.6.0", "root"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second request: %v", err)
	}
}

func TestUpgraderBusy(t *testing.T) {
	now := time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)
	u := newUpgrader(t, `{"state":"running","from":"v0.5.0","to":"v0.6.0","started_at":"2026-10-28T07:55:00Z"}`)
	u.Now = func() time.Time { return now }
	p, _ := u.Progress()
	if !u.Busy(p) || p.Started().IsZero() {
		t.Fatalf("running upgrade not busy: %+v", p)
	}
	if err := u.Request("v0.6.0", "root"); !errors.Is(err, ErrBusy) {
		t.Fatalf("request while running: %v", err)
	}
	// A run that never finished does not block a retry for ever.
	now = now.Add(time.Hour)
	if u.Busy(p) {
		t.Fatal("stale run still busy")
	}
	for _, state := range []string{StateSucceeded, StateFailed, StateReady} {
		if u.Busy(&Progress{State: state}) {
			t.Fatalf("%s busy", state)
		}
	}
}

func TestUpgraderLog(t *testing.T) {
	u := newUpgrader(t, `{"state":"failed"}`)
	if u.Log(10) != nil {
		t.Fatal("log without a file")
	}
	var b strings.Builder
	for i := range 100 {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%3))
		b.WriteString("\n")
	}
	b.WriteString("失败: 安装失败\n")
	if err := os.WriteFile(filepath.Join(u.Dir, "update.log"), []byte(b.String()), 0o640); err != nil {
		t.Fatal(err)
	}
	lines := u.Log(5)
	if len(lines) != 5 || lines[4] != "失败: 安装失败" {
		t.Fatalf("log %q", lines)
	}
}

// A request nobody picks up stops counting as busy, so it can be retried.
func TestUpgraderStalled(t *testing.T) {
	u := newUpgrader(t, `{"state":"ready","method":"package"}`)
	now := time.Now()
	u.Now = func() time.Time { return now }
	if err := u.Request("v0.6.0", "root"); err != nil {
		t.Fatal(err)
	}
	p, _ := u.Progress()
	if !u.Busy(p) || u.Stalled(p) {
		t.Fatalf("fresh request: busy %v stalled %v", u.Busy(p), u.Stalled(p))
	}
	now = now.Add(3 * time.Minute)
	if u.Busy(p) || !u.Stalled(p) {
		t.Fatalf("old request: busy %v stalled %v", u.Busy(p), u.Stalled(p))
	}
	if err := u.Request("v0.6.0", "root"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}
