package adminweb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/repository"
)

var reasonNames = map[string]string{
	// Risk reasons (verify risk_reasons).
	"WEBDRIVER": "自动化驱动（webdriver）", "HEADLESS_UA": "无头浏览器 UA", "AUTOMATION_GLOBALS": "自动化框架痕迹",
	"UNTRUSTED_EVENT": "脚本伪造的事件", "SOFTWARE_GL": "软件渲染 WebGL", "ZERO_OUTER": "窗口尺寸异常",
	"PERMISSION_MISMATCH": "权限状态矛盾", "UA_PLATFORM_MISMATCH": "UA 与平台不符", "NO_LANGUAGES": "没有语言设置",
	"DESKTOP_NO_PLUGINS": "桌面浏览器无插件", "CLICK_WITHOUT_MOVE": "点击前无鼠标移动", "FAST_CLICK": "点击过快",
	"NO_SIGNALS": "未上报浏览器信号", "UA_HEADER_MISMATCH": "UA 与请求头不一致", "IP_RECENT_FAILURES": "同 IP 近期失败",
	"IP_HIGH_RATE": "同 IP 请求频繁", "FALLBACK_CHOSEN": "选择了无障碍路径", "KEYBOARD_INPUT": "键盘完成拖动题",
	"TOUCH_INPUT": "触摸输入", "CAPACITY_DEGRADED": "图片题容量不足，降级为 PoW",
	// Verify rejections.
	"TOKEN_INVALID": "token 无效", "TOKEN_EXPIRED": "token 过期", "TOKEN_CONSUMED": "token 已被使用",
	"ACTION_MISMATCH": "action 不符", "ORIGIN_NOT_ALLOWED": "来源不允许", "APP_MISMATCH": "应用不符",
	// Browser-side errors.
	"PROOF_INVALID": "PoW 证明错误", "ANSWER_INVALID": "答案或轨迹未通过", "CHALLENGE_USED": "challenge 重复提交",
	"CHALLENGE_EXPIRED": "challenge 过期", "CHALLENGE_INVALID": "challenge 无效", "RATE_LIMITED": "触发限流",
	"INVALID_REQUEST": "请求格式错误", "ACTION_FORBIDDEN": "action 未启用", "APP_FORBIDDEN": "应用已停用",
	"UNAVAILABLE": "服务不可用", "UNAUTHORIZED": "签名无效", "TIMESTAMP_EXPIRED": "时间戳过期",
	"NONCE_REUSED": "请求重放", "IDEMPOTENCY_CONFLICT": "request_id 冲突", "pass": "通过",
}

func reasonText(code string) string {
	if n, ok := reasonNames[code]; ok {
		return n
	}
	return code
}

// Counts are summed counters with the derived figures the pages show.
type Counts map[string]int64

func (c Counts) Get(k string) int64 { return c[k] }

// Rate is a/b as a percentage string.
func (c Counts) Rate(a, b string) string { return formatPct(c[a], c[b]) }

// PassRate is solves that passed over all solves.
func (c Counts) PassRate() string { return formatPct(c["solve_ok"], c["solve"]) }

// Blocked counts every request stopped before a token: failed solves and
// refused issues.
func (c Counts) Blocked() int64 { return c["solve_fail"] + c["issue_fail"] }

// Ranked is one row of a distribution.
type Ranked struct {
	Code  string
	Label string
	Count int64
	Share float64 // percent of the list total, for legends
	Bar   float64 // percent of the largest row shown together, for bar widths
}

// scaleBars sets Bar against the largest count across lists shown in one card.
func scaleBars(lists ...[]Ranked) {
	var top int64 = 1
	for _, l := range lists {
		for _, r := range l {
			top = max(top, r.Count)
		}
	}
	for _, l := range lists {
		for i := range l {
			l[i].Bar = float64(l[i].Count) * 100 / float64(top)
		}
	}
}

// Prefixed collects fields "prefix:<code>" as a ranking, largest first.
func (c Counts) Prefixed(prefix string, limit int) []Ranked {
	var out []Ranked
	var total int64
	for k, v := range c {
		if code, ok := strings.CutPrefix(k, prefix+":"); ok && v > 0 {
			out = append(out, Ranked{Code: code, Count: v})
			total += v
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Code < out[j].Code
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	for i := range out {
		if l, ok := kindNames[out[i].Code]; ok && (prefix == "issue" || strings.HasPrefix(prefix, "solve_ok") || strings.HasSuffix(prefix, "kind")) {
			out[i].Label = l
		} else if l, ok := levelNames[out[i].Code]; ok && prefix == "risk" {
			out[i].Label = l + "风险"
		} else {
			out[i].Label = reasonText(out[i].Code)
		}
		out[i].Share = float64(out[i].Count) * 100 / float64(max(1, total))
	}
	scaleBars(out)
	return out
}

// Trend is a chart series for the SVG charts in console.js.
type Trend struct {
	Labels []string           `json:"labels"`
	Lines  map[string][]int64 `json:"lines"`
}

var trendFields = []string{"issue", "solve_ok", "solve_fail", "verify_pass", "verify_reject"}

// collect reads every app's hourly counters in [from, to]. It returns the
// sum per app, the sum over all apps, and the hourly (or, with daily, the
// per-day) trend.
func (s *Server) collect(ctx context.Context, apps []repository.AppSummary, from, to time.Time, daily bool) (map[int64]Counts, Counts, Trend, error) {
	perApp := map[int64]Counts{}
	all := Counts{}
	type bucket struct {
		label string
		c     Counts
	}
	var buckets []*bucket
	index := map[string]*bucket{}
	label := func(t time.Time) string {
		t = t.In(cst)
		if daily {
			return t.Format("01-02")
		}
		if from.Before(to.Add(-26 * time.Hour)) {
			return t.Format("01-02 15时")
		}
		return t.Format("15:00")
	}
	for t := from.UTC().Truncate(time.Hour); !t.After(to); t = t.Add(time.Hour) {
		l := label(t)
		if _, ok := index[l]; !ok {
			b := &bucket{label: l, c: Counts{}}
			index[l] = b
			buckets = append(buckets, b)
		}
	}
	for _, a := range apps {
		hours, err := s.Stats.Hours(ctx, a.ID, from, to)
		if err != nil {
			return nil, nil, Trend{}, err
		}
		c := Counts{}
		for _, h := range hours {
			b := index[label(h.Start)]
			for k, v := range h.Counts {
				c[k] += v
				all[k] += v
				if b != nil {
					b.c[k] += v
				}
			}
		}
		perApp[a.ID] = c
	}
	tr := Trend{Lines: map[string][]int64{}}
	for _, b := range buckets {
		tr.Labels = append(tr.Labels, b.label)
		for _, f := range trendFields {
			tr.Lines[f] = append(tr.Lines[f], b.c[f])
		}
	}
	return perApp, all, tr, nil
}

// AppRow is one app in a ranking table.
type AppRow struct {
	App    repository.AppSummary
	Counts Counts
}

func rankApps(apps []repository.AppSummary, perApp map[int64]Counts) []AppRow {
	rows := make([]AppRow, 0, len(apps))
	for _, a := range apps {
		rows = append(rows, AppRow{App: a, Counts: perApp[a.ID]})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Counts["issue"] > rows[j].Counts["issue"] })
	return rows
}

// RecentEvent is an event with the app it belongs to.
type RecentEvent struct {
	repository.Event
	AppKey  string
	AppName string
}

func (s *Server) recentEvents(ctx context.Context, apps []repository.AppSummary, limit int, filter func(repository.Event) bool) ([]RecentEvent, error) {
	var out []RecentEvent
	for _, a := range apps {
		evs, err := s.Stats.Events(ctx, a.ID, limit*4)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			if filter == nil || filter(e) {
				out = append(out, RecentEvent{Event: e, AppKey: a.AppKey, AppName: a.Name})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// kindOrder lists kinds for distribution charts, the invisible path first.
var kindOrder = append([]string{domain.KindInvisible}, append(append([]string{}, domain.InteractiveKinds...), domain.KindFallback)...)

// DetailItem is one field of an audit record's detail, labelled for people.
type DetailItem struct{ Label, Value string }

var detailLabels = map[string]string{
	"action": "action", "enabled": "状态", "mode": "出题方式", "kinds": "题型", "client_select": "页面指定题型", "puzzles": "PoW 子题数", "difficulty": "每题难度",
	"origin": "来源", "name": "名称", "remark": "备注", "role": "角色", "username": "用户名", "new_secret_id": "新密钥对",
	"grace": "宽限期", "expires_at": "过期时间", "totp": "动态码", "mfa": "动态码", "reason": "原因", "display_name": "显示名称", "app_key": "应用",
}

var detailOrder = []string{"username", "name", "action", "origin", "enabled", "mode", "kinds", "client_select", "puzzles", "difficulty", "role",
	"display_name", "remark", "app_key", "new_secret_id", "grace", "expires_at", "totp", "mfa", "reason"}

var detailReasons = map[string]string{"unknown_user": "用户不存在", "bad_password": "密码错误", "disabled": "账号已停用",
	"bad_otp": "动态码错误", "otp_reused": "动态码重复使用"}

// auditDetail turns an audit record's JSON detail into labelled items.
func auditDetail(raw string) []DetailItem {
	var m map[string]any
	if raw == "" || json.Unmarshal([]byte(raw), &m) != nil {
		if raw == "" {
			return nil
		}
		return []DetailItem{{Label: "详情", Value: raw}}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Business order first, anything else alphabetically after it.
	rank := func(k string) int {
		for i, o := range detailOrder {
			if o == k {
				return i
			}
		}
		return len(detailOrder)
	}
	sort.Slice(keys, func(i, j int) bool {
		if ri, rj := rank(keys[i]), rank(keys[j]); ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	out := make([]DetailItem, 0, len(m))
	for _, k := range keys {
		label := detailLabels[k]
		if label == "" {
			label = k
		}
		var val string
		switch v := m[k].(type) {
		case bool:
			val = map[bool]string{true: "启用", false: "停用"}[v]
			switch k {
			case "totp":
				val = map[bool]string{true: "已开启", false: "未开启"}[v]
			case "mfa":
				val = map[bool]string{true: "已验证", false: "未使用"}[v]
			}
		case float64:
			val = formatNum(v)
		case []any:
			var parts []string
			for _, x := range v {
				s := fmt.Sprint(x)
				if n, ok := kindNames[s]; ok {
					s = n
				}
				parts = append(parts, s)
			}
			val = strings.Join(parts, "、")
		case nil:
			val = "—"
		default:
			val = fmt.Sprint(v)
			switch k {
			case "mode":
				if n, ok := modeNames[val]; ok {
					val = n
				}
			case "role":
				if n, ok := logic.RoleNames[val]; ok {
					val = n
				}
			case "reason":
				if n, ok := detailReasons[val]; ok {
					val = n
				}
			}
		}
		out = append(out, DetailItem{Label: label, Value: val})
	}
	return out
}

var pointerNames = map[string]string{"mouse": "鼠标", "touch": "触摸", "pen": "触控笔", "keyboard": "键盘"}
