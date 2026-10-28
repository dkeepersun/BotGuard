package update

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dkeepersun/BotGuard/internal/version"
)

// Where scripts/botguard.sh install-systemd puts the one-click upgrade
// (docs/admin.md 一键升级): the console writes RequestFile as the service
// user; botguard-update.path then runs `botguard self-update` as root, which
// writes status.json and update.log in Dir. The console can only read Dir.
const (
	DefaultRequestFile = "/var/lib/botguard/update-request"
	DefaultDir         = "/var/lib/botguard-update"
)

// Updater states in status.json.
const (
	StateReady     = "ready"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
)

// staleRun is how long a run may stay "running" before the console stops
// waiting for it (the updater times out after 20 minutes). A request nobody
// picks up within stalePending means the updater is not running.
const (
	staleRun     = 30 * time.Minute
	stalePending = 2 * time.Minute
)

// ErrBusy means a request is waiting or an upgrade is running.
var ErrBusy = errors.New("已有一个升级正在进行")

// Progress is the updater's status.json, plus whether a request is waiting.
type Progress struct {
	State       string `json:"state"`
	Method      string `json:"method"` // package (deb/rpm) or binary (release tarball)
	From        string `json:"from"`
	To          string `json:"to"`
	RequestedBy string `json:"requested_by"`
	Message     string `json:"message"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at"`
	// Pending: a request is written and the updater has not picked it up.
	Pending      bool      `json:"-"`
	PendingSince time.Time `json:"-"`
}

// Started returns StartedAt as a time; zero when unknown.
func (p *Progress) Started() time.Time { t, _ := time.Parse(time.RFC3339, p.StartedAt); return t }

// Finished returns FinishedAt as a time; zero when unknown.
func (p *Progress) Finished() time.Time { t, _ := time.Parse(time.RFC3339, p.FinishedAt); return t }

// Upgrader hands upgrade requests to the root-owned updater and reads its
// progress. A nil Upgrader means one-click upgrades are not offered.
type Upgrader struct {
	RequestFile string
	Dir         string
	Now         func() time.Time
}

func (u *Upgrader) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

// Progress reads the updater's status. It returns nil without error when
// the updater is not installed (source and Docker installs, macOS).
func (u *Upgrader) Progress() (*Progress, error) {
	b, err := os.ReadFile(filepath.Join(u.Dir, "status.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Progress
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("status.json: %w", err)
	}
	if st, err := os.Lstat(u.RequestFile); err == nil {
		p.Pending, p.PendingSince = true, st.ModTime()
	}
	return &p, nil
}

// Busy reports whether a request is waiting or a run is in progress. A run
// that has said "running" for longer than the updater may take is presumed
// killed, so it does not block a retry forever.
func (u *Upgrader) Busy(p *Progress) bool {
	if p == nil {
		return false
	}
	if p.Pending {
		return !u.Stalled(p)
	}
	return p.State == StateRunning && u.now().Sub(p.Started()) < staleRun
}

// Stalled reports a request the updater has not picked up in time: its
// path unit is stopped or failed. A new request replaces it.
func (u *Upgrader) Stalled(p *Progress) bool {
	return p != nil && p.Pending && u.now().Sub(p.PendingSince) >= stalePending
}

var userPattern = regexp.MustCompile(`^[a-z0-9_.-]{1,32}$`)

// Request asks the updater to install target, a final release such as
// v0.6.0. The updater checks again that target is the latest release and
// newer than the installed one, and uses nothing else from the file.
func (u *Upgrader) Request(target, by string) error {
	if !version.Valid(target) || strings.Contains(target, "-") {
		return fmt.Errorf("不是正式版本号：%q", target)
	}
	if !userPattern.MatchString(by) {
		by = "unknown"
	}
	p, err := u.Progress()
	if err != nil {
		return err
	}
	if p == nil {
		return errors.New("这台服务器没有安装一键升级（botguard-update.path）")
	}
	if u.Busy(p) {
		return ErrBusy
	}
	body := fmt.Sprintf("version=%s\nrequested_by=%s\nrequested_at=%s\n", target, by, u.now().UTC().Format(time.RFC3339))
	// Written beside the target and renamed into place, so the path unit
	// never starts the updater on half a file.
	f, err := os.CreateTemp(filepath.Dir(u.RequestFile), ".update-request-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, u.RequestFile)
}

// Log returns the last n lines of the latest run's log.
func (u *Upgrader) Log(n int) []string {
	f, err := os.Open(filepath.Join(u.Dir, "update.log"))
	if err != nil {
		return nil
	}
	defer f.Close()
	const max = 64 << 10
	if st, err := f.Stat(); err == nil && st.Size() > max {
		_, _ = f.Seek(st.Size()-max, io.SeekStart)
	}
	b, _ := io.ReadAll(io.LimitReader(f, max))
	lines := strings.Split(strings.TrimRight(string(bytes.ToValidUTF8(b, []byte("?"))), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}
