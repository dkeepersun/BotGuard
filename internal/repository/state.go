package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/stores/redis"
)

// Verify outcomes stored in the idempotency record. PassPrefix is followed by
// the verified_at unix second; the others are contract reason codes.
const (
	PassPrefix     = "PASS:"
	ReasonInvalid  = "TOKEN_INVALID"
	ReasonExpired  = "TOKEN_EXPIRED"
	ReasonConsumed = "TOKEN_CONSUMED"
	ReasonAction   = "ACTION_MISMATCH"
	ReasonOrigin   = "ORIGIN_NOT_ALLOWED"
	ReasonApp      = "APP_MISMATCH"
)

var ErrIdempotencyConflict = errors.New("request_id reused with a different request")

// verifyScript atomically replays a prior result, or checks and consumes the
// token and records the outcome. Rejections are recorded too, so a retry of
// the same request always gets the same answer.
//
// KEYS[1] result key, KEYS[2] token key
// ARGV[1] request digest, ARGV[2] now, ARGV[3] result ttl, ARGV[4] action,
// ARGV[5] forced rejection reason or ”, ARGV[6..] allowed origin patterns,
// matched as domain.OriginMatches does
//
// A pass is PASS:verified_at:kind:level:reasons:origin; origin is last since
// it contains colons.
const verifyScript = `
local prior = redis.call('GET', KEYS[1])
if prior then
  local sep = string.find(prior, '|', 1, true)
  if string.sub(prior, 1, sep - 1) ~= ARGV[1] then
    return {'CONFLICT', ''}
  end
  return {'REPLAY', string.sub(prior, sep + 1)}
end
local function finish(outcome)
  redis.call('SET', KEYS[1], ARGV[1] .. '|' .. outcome, 'EX', ARGV[3])
  return {'NEW', outcome}
end
if ARGV[5] ~= '' then return finish(ARGV[5]) end
local t = redis.call('HMGET', KEYS[2], 'action', 'origin', 'exp', 'consumed', 'kind', 'level', 'reasons')
if not t[1] then return finish('TOKEN_INVALID') end
if t[4] == '1' then return finish('TOKEN_CONSUMED') end
if tonumber(t[3]) <= tonumber(ARGV[2]) then return finish('TOKEN_EXPIRED') end
if t[1] ~= ARGV[4] then return finish('ACTION_MISMATCH') end
local function matches(p, o)
  if p == '*' then return true end
  local i = string.find(p, '://*.', 1, true)
  if not i then return p == o end
  local prefix, suffix = string.sub(p, 1, i + 2), string.sub(p, i + 4)
  return #o > #prefix + #suffix and string.sub(o, 1, #prefix) == prefix and string.sub(o, -#suffix) == suffix
end
local allowed = false
for i = 6, #ARGV do
  if matches(ARGV[i], t[2]) then allowed = true break end
end
if not allowed then return finish('ORIGIN_NOT_ALLOWED') end
redis.call('HSET', KEYS[2], 'consumed', '1')
return finish('PASS:' .. ARGV[2] .. ':' .. (t[5] or '') .. ':' .. (t[6] or '') .. ':' .. (t[7] or '') .. ':' .. t[2])
`

const putTokenScript = `
redis.call('HSET', KEYS[1], 'action', ARGV[1], 'origin', ARGV[2], 'exp', ARGV[3], 'consumed', '0',
  'kind', ARGV[5], 'level', ARGV[6], 'reasons', ARGV[7])
redis.call('EXPIRE', KEYS[1], ARGV[4])
return 1
`

// Scripts run with EVAL rather than EVALSHA: go-zero's breaker counts NOSCRIPT
// as a failure, so a cold script cache after a Redis restart would open the
// breaker under concurrent load. The scripts are small enough that resending
// them costs little.

// State is the Redis-backed short-lived verification state.
type State struct {
	rds *redis.Redis
}

func NewState(rds *redis.Redis) *State { return &State{rds: rds} }

func usedKey(challengeID string) string { return "bg:pow:used:" + challengeID }

// Keys for one app share a hash tag so the verify script stays single-slot
// under Redis Cluster.
func tokenKey(appID int64, tokenHash string) string {
	return fmt.Sprintf("bg:{%d}:token:%s", appID, tokenHash)
}

func resultKey(appID int64, requestID string) string {
	return fmt.Sprintf("bg:{%d}:verify:%s", appID, requestID)
}

const failScript = `
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
return n
`

func failKey(ipBucket string) string { return "bg:ipfail:" + ipBucket }

// RecordFailure counts a failed solve for an IP bucket within window.
func (s *State) RecordFailure(ctx context.Context, ipBucket string, window time.Duration) error {
	_, err := s.rds.EvalCtx(ctx, failScript, []string{failKey(ipBucket)}, ceilSeconds(window))
	return err
}

// ladderScript moves a client's rung on the kind ladder: up one on a failure
// (resetting the pass streak), down one after ARGV[2] passes in a row.
//
// KEYS[1] ladder key; ARGV[1] "1" passed / "0" failed, ARGV[2] passes per
// step down, ARGV[3] top rung, ARGV[4] ttl seconds
const ladderScript = `
local level = tonumber(redis.call('HGET', KEYS[1], 'level') or '0')
local streak = tonumber(redis.call('HGET', KEYS[1], 'streak') or '0')
if ARGV[1] == '1' then
  streak = streak + 1
  if streak >= tonumber(ARGV[2]) then
    if level > 0 then level = level - 1 end
    streak = 0
  end
else
  level = math.min(level + 1, tonumber(ARGV[3]))
  streak = 0
end
redis.call('HSET', KEYS[1], 'level', level, 'streak', streak)
redis.call('EXPIRE', KEYS[1], ARGV[4])
return level
`

func ladderKey(appID int64, ipBucket string) string {
	return fmt.Sprintf("bg:{%d}:ladder:%s", appID, ipBucket)
}

// LadderLevel returns the client's rung on an app's kind ladder, 0 if none.
func (s *State) LadderLevel(ctx context.Context, appID int64, ipBucket string) (int, error) {
	v, err := s.rds.HgetCtx(ctx, ladderKey(appID, ipBucket), "level")
	if errors.Is(err, redis.Nil) || (err == nil && v == "") {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

// RecordLadder moves the client's rung after a solve; the state lives for
// window after the last solve.
func (s *State) RecordLadder(ctx context.Context, appID int64, ipBucket string, passed bool, passesPerStep, top int, window time.Duration) error {
	ok := "0"
	if passed {
		ok = "1"
	}
	_, err := s.rds.EvalCtx(ctx, ladderScript, []string{ladderKey(appID, ipBucket)}, ok, max(1, passesPerStep), top, ceilSeconds(window))
	return err
}

// Failures returns the recent failed solves for an IP bucket.
func (s *State) Failures(ctx context.Context, ipBucket string) (int, error) {
	v, err := s.rds.GetCtx(ctx, failKey(ipBucket))
	if err != nil || v == "" {
		return 0, err
	}
	return strconv.Atoi(v)
}

// MarkNonce reports false when this secret already used the nonce within ttl,
// i.e. the signed request is a replay.
func (s *State) MarkNonce(ctx context.Context, secretID, nonce string, ttl time.Duration) (bool, error) {
	return s.rds.SetnxExCtx(ctx, "bg:nonce:"+secretID+":"+nonce, "1", ceilSeconds(ttl))
}

// MarkChallengeUsed reports false when the challenge was already submitted.
func (s *State) MarkChallengeUsed(ctx context.Context, challengeID string, ttl time.Duration) (bool, error) {
	return s.rds.SetnxExCtx(ctx, usedKey(challengeID), "1", ceilSeconds(ttl))
}

type TokenRecord struct {
	Action    string
	Origin    string
	ExpiresAt int64
	// How the token was earned, reported to the business server on verify.
	Kind      string
	RiskLevel string
	Reasons   []string
}

// PutToken keeps the record for keyTTL (longer than the logical expiry) so
// verify can tell expired and consumed tokens apart from unknown ones.
func (s *State) PutToken(ctx context.Context, appID int64, tokenHash string, rec TokenRecord, keyTTL time.Duration) error {
	_, err := s.rds.EvalCtx(ctx, putTokenScript, []string{tokenKey(appID, tokenHash)},
		rec.Action, rec.Origin, strconv.FormatInt(rec.ExpiresAt, 10), strconv.Itoa(ceilSeconds(keyTTL)),
		rec.Kind, rec.RiskLevel, strings.Join(rec.Reasons, ","))
	return err
}

type ConsumeInput struct {
	AppID          int64
	RequestID      string
	RequestDigest  string
	TokenHash      string
	Action         string
	AllowedOrigins []string
	// Forced records this rejection without touching the token, for tokens
	// rejected before lookup (malformed, other app). It still goes through
	// the idempotency record so request_id conflicts are detected.
	Forced    string
	Now       int64
	ResultTTL time.Duration
}

// Consume returns the stored outcome (PassPrefix+verified_at or a reason) and
// whether it was replayed from an earlier identical request.
func (s *State) Consume(ctx context.Context, in ConsumeInput) (outcome string, replayed bool, err error) {
	args := []any{in.RequestDigest, strconv.FormatInt(in.Now, 10), strconv.Itoa(ceilSeconds(in.ResultTTL)), in.Action, in.Forced}
	for _, o := range in.AllowedOrigins {
		args = append(args, o)
	}
	res, err := s.rds.EvalCtx(ctx, verifyScript,
		[]string{resultKey(in.AppID, in.RequestID), tokenKey(in.AppID, in.TokenHash)}, args...)
	if err != nil {
		return "", false, err
	}
	parts, ok := res.([]any)
	if !ok || len(parts) != 2 {
		return "", false, fmt.Errorf("verify script returned %T", res)
	}
	kind, _ := parts[0].(string)
	outcome, _ = parts[1].(string)
	switch kind {
	case "CONFLICT":
		return "", false, ErrIdempotencyConflict
	case "REPLAY", "NEW":
		if outcome == "" {
			return "", false, fmt.Errorf("verify script returned outcome %q", outcome)
		}
		return outcome, kind == "REPLAY", nil
	default:
		return "", false, fmt.Errorf("verify script returned kind %q", kind)
	}
}

func (s *State) Ping(ctx context.Context) bool { return s.rds.PingCtx(ctx) }

func ceilSeconds(d time.Duration) int {
	s := int((d + time.Second - 1) / time.Second)
	if s < 1 {
		return 1
	}
	return s
}
