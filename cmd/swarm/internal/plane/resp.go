package plane

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RESP is the store for peers that share no filesystem: any server that
// speaks the Redis protocol and runs Lua (Redis, Valkey). Every transition
// is one script, so it is one atomic step on the server, and it reads the
// server's clock, so no peer's clock is trusted. Run the server with
// appendonly yes and appendfsync always if an accepted commit must survive
// the server's own crash.
//
// The client is deliberately small: one connection, redialed on failure,
// with the command retried. Retrying is safe because every mutating call
// carries an idempotency key; a reply lost to a dropped connection is
// answered from the record when the call is made again.
type RESP struct {
	addr   string
	prefix string
	mu     sync.Mutex
	conn   net.Conn
	rd     *bufio.Reader
	// RetryFor bounds how long a call keeps redialing a server that is
	// down. The fault harness restarts the server under load.
	RetryFor time.Duration
}

// OpenRESP connects to addr. prefix namespaces every key.
func OpenRESP(addr, prefix string) (*RESP, error) {
	r := &RESP{addr: addr, prefix: prefix, RetryFor: 30 * time.Second}
	if _, err := r.do("PING"); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RESP) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

// script is every transition. It mirrors core.go line for line; the
// conformance suite and the fault harness hold the two to one contract.
const script = `
local op, p = ARGV[1], ARGV[2]
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])
-- cjson writes large numbers as lossy floats; times cross the wire as exact strings.
local function S(n) return string.format('%.0f', n) end
local function ikey(kind, id) return p .. 'item:' .. kind .. '/' .. id end
local function log(e)
  e.seq = redis.call('INCR', p .. 'seq')
  e.at = S(now)
  redis.call('RPUSH', p .. 'log', cjson.encode(e))
end
local function put(kind, id, payload, parent)
  local k = ikey(kind, id)
  if redis.call('EXISTS', k) == 1 then return end
  redis.call('HSET', k, 'kind', kind, 'id', id, 'state', 'pending', 'payload', payload or '', 'epoch', 0,
    'owner', '', 'until', 0, 'version', 1, 'result', '', 'done_by', '', 'done_epoch', 0, 'parent', parent or '')
  redis.call('RPUSH', p .. 'order:' .. kind, id)
  log({op = 'put', kind = kind, id = id, ok = true, by = parent or ''})
end
local function claimable(k, inc)
  local state, owner, untl = unpack(redis.call('HMGET', k, 'state', 'owner', 'until'))
  if state == 'done' then return 'done' end
  if owner ~= '' and owner ~= inc and now < tonumber(untl) then return 'held' end
  return nil
end
local function grant(kind, id, inc, ttl, idem)
  local k = ikey(kind, id)
  local epoch = redis.call('HINCRBY', k, 'epoch', 1)
  local untl = now + ttl * 1000
  redis.call('HSET', k, 'owner', inc, 'until', untl)
  redis.call('HINCRBY', k, 'version', 1)
  local g = {kind = kind, id = id, epoch = epoch, owner = inc, until_us = S(untl), payload = redis.call('HGET', k, 'payload')}
  if idem ~= '' then redis.call('HSET', p .. 'claims', idem, cjson.encode(g)) end
  log({op = 'claim', kind = kind, id = id, by = inc, epoch = epoch, ok = true, idem = idem, until_us = S(untl)})
  return cjson.encode({ok = true, grant = g})
end
local function replayClaim(inc, idem)
  if idem == '' then return nil end
  local prior = redis.call('HGET', p .. 'claims', idem)
  if not prior then return nil end
  local g = cjson.decode(prior)
  log({op = 'claim', kind = g.kind, id = g.id, by = inc, epoch = g.epoch, ok = true, idem = idem, replay = true})
  return cjson.encode({ok = true, grant = g})
end

if op == 'put' then
  put(ARGV[3], ARGV[4], ARGV[5], '')
  return cjson.encode({ok = true})
end

if op == 'claim' then
  local kind, id, inc, ttl, idem = ARGV[3], ARGV[4], ARGV[5], tonumber(ARGV[6]), ARGV[7]
  local r = replayClaim(inc, idem); if r then return r end
  local k = ikey(kind, id)
  if redis.call('EXISTS', k) == 0 then return cjson.encode({ok = false, code = 'not_found'}) end
  local why = claimable(k, inc)
  if why then
    log({op = 'claim', kind = kind, id = id, by = inc, ok = false, code = why, idem = idem})
    return cjson.encode({ok = false, code = why})
  end
  return grant(kind, id, inc, ttl, idem)
end

if op == 'claimnext' then
  local kind, inc, ttl, idem = ARGV[3], ARGV[5], tonumber(ARGV[6]), ARGV[7]
  local r = replayClaim(inc, idem); if r then return r end
  for _, id in ipairs(redis.call('LRANGE', p .. 'order:' .. kind, 0, -1)) do
    if not claimable(ikey(kind, id), inc) then return grant(kind, id, inc, ttl, idem) end
  end
  return cjson.encode({ok = false, code = 'none'})
end

local function lease(kind, id)
  local k = ikey(kind, id)
  if redis.call('EXISTS', k) == 0 then return nil end
  local state, epoch, owner, untl = unpack(redis.call('HMGET', k, 'state', 'epoch', 'owner', 'until'))
  return k, state, tonumber(epoch), owner, tonumber(untl)
end

if op == 'renew' then
  local kind, id, inc, ttl, epoch = ARGV[3], ARGV[4], ARGV[5], tonumber(ARGV[6]), tonumber(ARGV[8])
  local k, state, cur, owner, untl = lease(kind, id)
  if not k then return cjson.encode({ok = false, code = 'not_found'}) end
  if state == 'done' then return cjson.encode({ok = false, code = 'done'}) end
  local why = nil
  if cur ~= epoch or owner ~= inc then why = 'fenced' elseif now >= untl then why = 'expired' end
  if why then
    log({op = 'renew', kind = kind, id = id, by = inc, epoch = epoch, ok = false, code = why})
    return cjson.encode({ok = false, code = why})
  end
  local nu = now + ttl * 1000
  redis.call('HSET', k, 'until', nu)
  redis.call('HINCRBY', k, 'version', 1)
  log({op = 'renew', kind = kind, id = id, by = inc, epoch = epoch, ok = true, until_us = S(nu)})
  return cjson.encode({ok = true, until_us = S(nu)})
end

if op == 'release' then
  local kind, id, inc, epoch = ARGV[3], ARGV[4], ARGV[5], tonumber(ARGV[8])
  local k, state, cur, owner = lease(kind, id)
  if not k then return cjson.encode({ok = false, code = 'not_found'}) end
  if cur ~= epoch or owner ~= inc then
    log({op = 'release', kind = kind, id = id, by = inc, epoch = epoch, ok = false, code = 'fenced'})
    return cjson.encode({ok = false, code = 'fenced'})
  end
  redis.call('HSET', k, 'owner', '', 'until', 0)
  redis.call('HINCRBY', k, 'version', 1)
  log({op = 'release', kind = kind, id = id, by = inc, epoch = epoch, ok = true})
  return cjson.encode({ok = true})
end

if op == 'commit' then
  local kind, id, inc, idem, epoch, result = ARGV[3], ARGV[4], ARGV[5], ARGV[7], tonumber(ARGV[8]), ARGV[9]
  if idem ~= '' then
    local prior = redis.call('HGET', p .. 'commits', idem)
    if prior then
      local r = cjson.decode(prior)
      log({op = 'commit', kind = r.kind, id = r.id, by = inc, epoch = r.epoch, ok = r.accepted, idem = idem, replay = true, result = r.result})
      return cjson.encode({ok = r.accepted, code = (r.accepted and '' or 'done'), receipt = r})
    end
  end
  local k, state, cur, owner = lease(kind, id)
  if not k then return cjson.encode({ok = false, code = 'not_found'}) end
  if state == 'done' then
    local de, db, res = unpack(redis.call('HMGET', k, 'done_epoch', 'done_by', 'result'))
    local r = {kind = kind, id = id, accepted = false, epoch = tonumber(de), winner = db, result = res, emitted = 0}
    if idem ~= '' then redis.call('HSET', p .. 'commits', idem, cjson.encode(r)) end
    log({op = 'commit', kind = kind, id = id, by = inc, epoch = epoch, ok = false, code = 'done', idem = idem})
    return cjson.encode({ok = false, code = 'done', receipt = r})
  end
  if cur ~= epoch or owner ~= inc then
    log({op = 'commit', kind = kind, id = id, by = inc, epoch = epoch, ok = false, code = 'fenced', idem = idem})
    return cjson.encode({ok = false, code = 'fenced'})
  end
  redis.call('HSET', k, 'state', 'done', 'result', result, 'done_by', inc, 'done_epoch', epoch, 'owner', '', 'until', 0)
  redis.call('HINCRBY', k, 'version', 1)
  log({op = 'commit', kind = kind, id = id, by = inc, epoch = epoch, ok = true, idem = idem, result = result})
  local emit = cjson.decode(ARGV[10])
  for _, e in ipairs(emit) do put(e.kind, e.id, e.payload, kind .. '/' .. id) end
  local r = {kind = kind, id = id, accepted = true, epoch = epoch, winner = inc, result = result, emitted = #emit}
  if idem ~= '' then redis.call('HSET', p .. 'commits', idem, cjson.encode(r)) end
  return cjson.encode({ok = true, receipt = r})
end

return cjson.encode({ok = false, code = 'bad_op'})
`

type respGrant struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Epoch   int64  `json:"epoch"`
	Owner   string `json:"owner"`
	UntilUS string `json:"until_us"`
	Payload string `json:"payload"`
}

type respReply struct {
	OK      bool       `json:"ok"`
	Code    string     `json:"code"`
	Grant   *respGrant `json:"grant"`
	UntilUS string     `json:"until_us"`
	Receipt *Receipt   `json:"receipt"`
}

func fromCode(c string) error {
	switch c {
	case "held":
		return ErrHeld
	case "fenced":
		return ErrFenced
	case "done":
		return ErrDone
	case "expired":
		return ErrExpired
	case "none":
		return ErrNone
	case "not_found":
		return ErrNotFound
	}
	return fmt.Errorf("plane: store refused: %s", c)
}

func us(t int64) time.Time { return time.UnixMicro(t).UTC() }

func usStr(v string) time.Time {
	n, _ := strconv.ParseInt(v, 10, 64)
	return us(n)
}

func (r *RESP) eval(op, kind, id, inc string, ttl time.Duration, idem string, epoch int64, result string, emit []Item) (*respReply, error) {
	if emit == nil {
		emit = []Item{}
	}
	ej, _ := json.Marshal(emit)
	raw, err := r.do("EVAL", script, "0", op, r.prefix, kind, id, inc, strconv.FormatInt(ttl.Milliseconds(), 10), idem, strconv.FormatInt(epoch, 10), result, string(ej))
	if err != nil {
		return nil, err
	}
	var rep respReply
	if err := json.Unmarshal([]byte(raw.(string)), &rep); err != nil {
		return nil, fmt.Errorf("plane: bad reply %q: %w", raw, err)
	}
	return &rep, nil
}

func (r *RESP) Put(_ context.Context, it Item) error {
	_, err := r.evalPut(it)
	return err
}

func (r *RESP) evalPut(it Item) (*respReply, error) {
	raw, err := r.do("EVAL", script, "0", "put", r.prefix, it.Kind, it.ID, it.Payload)
	if err != nil {
		return nil, err
	}
	var rep respReply
	return &rep, json.Unmarshal([]byte(raw.(string)), &rep)
}

func (r *RESP) grantOf(rep *respReply, err error) (Grant, error) {
	if err != nil {
		return Grant{}, err
	}
	if !rep.OK {
		return Grant{}, fromCode(rep.Code)
	}
	g := rep.Grant
	return Grant{Kind: g.Kind, ID: g.ID, Epoch: g.Epoch, Owner: g.Owner, Until: usStr(g.UntilUS), Payload: g.Payload}, nil
}

func (r *RESP) Claim(_ context.Context, kind, id, inc string, ttl time.Duration, idem string) (Grant, error) {
	return r.grantOf(r.eval("claim", kind, id, inc, ttl, idem, 0, "", nil))
}

func (r *RESP) ClaimNext(_ context.Context, kind, inc string, ttl time.Duration, idem string) (Grant, error) {
	return r.grantOf(r.eval("claimnext", kind, "", inc, ttl, idem, 0, "", nil))
}

func (r *RESP) Renew(_ context.Context, g Grant, ttl time.Duration) (Grant, error) {
	rep, err := r.eval("renew", g.Kind, g.ID, g.Owner, ttl, "", g.Epoch, "", nil)
	if err != nil {
		return Grant{}, err
	}
	if !rep.OK {
		return Grant{}, fromCode(rep.Code)
	}
	g.Until = usStr(rep.UntilUS)
	return g, nil
}

func (r *RESP) Release(_ context.Context, g Grant) error {
	rep, err := r.eval("release", g.Kind, g.ID, g.Owner, 0, "", g.Epoch, "", nil)
	if err != nil {
		return err
	}
	if !rep.OK {
		return fromCode(rep.Code)
	}
	return nil
}

func (r *RESP) Commit(_ context.Context, g Grant, result, idem string, emit []Item) (Receipt, error) {
	rep, err := r.eval("commit", g.Kind, g.ID, g.Owner, 0, idem, g.Epoch, result, emit)
	if err != nil {
		return Receipt{}, err
	}
	var rc Receipt
	if rep.Receipt != nil {
		rc = *rep.Receipt
	}
	if !rep.OK {
		return rc, fromCode(rep.Code)
	}
	return rc, nil
}

func (r *RESP) Get(_ context.Context, kind, id string) (Item, error) {
	raw, err := r.do("HGETALL", r.prefix+"item:"+kind+"/"+id)
	if err != nil {
		return Item{}, err
	}
	arr, _ := raw.([]any)
	if len(arr) == 0 {
		return Item{}, ErrNotFound
	}
	m := map[string]string{}
	for i := 0; i+1 < len(arr); i += 2 {
		m[arr[i].(string)] = arr[i+1].(string)
	}
	n := func(k string) int64 { v, _ := strconv.ParseInt(m[k], 10, 64); return v }
	it := Item{Kind: m["kind"], ID: m["id"], State: m["state"], Payload: m["payload"], Epoch: n("epoch"), Owner: m["owner"],
		Version: n("version"), Result: m["result"], DoneBy: m["done_by"], DoneEpoch: n("done_epoch"), Parent: m["parent"]}
	if u := n("until"); u > 0 {
		it.Until = us(u)
	}
	return it, nil
}

func (r *RESP) List(ctx context.Context, kind string) ([]Item, error) {
	raw, err := r.do("LRANGE", r.prefix+"order:"+kind, "0", "-1")
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, id := range raw.([]any) {
		it, err := r.Get(ctx, kind, id.(string))
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, nil
}

func (r *RESP) History(context.Context) ([]Event, error) {
	raw, err := r.do("LRANGE", r.prefix+"log", "0", "-1")
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, line := range raw.([]any) {
		// at and until arrive as server microseconds; decode them apart from
		// the time.Time fields they would otherwise fail to parse into.
		var m map[string]any
		if err := json.Unmarshal([]byte(line.(string)), &m); err != nil {
			return nil, err
		}
		e := Event{Op: str(m["op"]), Kind: str(m["kind"]), ID: str(m["id"]), By: str(m["by"]), Code: str(m["code"]),
			Idem: str(m["idem"]), Result: str(m["result"]), Seq: num(m["seq"]), Epoch: num(m["epoch"]), At: usStr(str(m["at"]))}
		e.OK, _ = m["ok"].(bool)
		e.Replay, _ = m["replay"].(bool)
		if u := str(m["until_us"]); u != "" {
			e.Until = usStr(u)
		}
		out = append(out, e)
	}
	return out, nil
}

func str(v any) string { s, _ := v.(string); return s }
func num(v any) int64  { f, _ := v.(float64); return int64(f) }

// do sends one command, redialing and retrying while the server is away.
func (r *RESP) do(args ...string) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	deadline := time.Now().Add(r.RetryFor)
	for {
		v, err := r.once(args)
		var se serverError
		if err == nil || errors.As(err, &se) {
			if isLoading(err) && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return v, err
		}
		if r.conn != nil {
			_ = r.conn.Close()
			r.conn = nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("plane: store unreachable at %s: %w", r.addr, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type serverError string

func (e serverError) Error() string { return string(e) }

func isLoading(err error) bool {
	var se serverError
	return errors.As(err, &se) && strings.HasPrefix(string(se), "LOADING")
}

func (r *RESP) once(args []string) (any, error) {
	if r.conn == nil {
		c, err := net.DialTimeout("tcp", r.addr, 2*time.Second)
		if err != nil {
			return nil, err
		}
		r.conn, r.rd = c, bufio.NewReader(c)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&sb, "$%d\r\n%s\r\n", len(a), a)
	}
	_ = r.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(r.conn, sb.String()); err != nil {
		return nil, err
	}
	return readReply(r.rd)
}

func readReply(rd *bufio.Reader) (any, error) {
	line, err := rd.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil, io.ErrUnexpectedEOF
	}
	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return nil, serverError(line[1:])
	case ':':
		n, err := strconv.ParseInt(line[1:], 10, 64)
		return n, err
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil || n < 0 {
			return nil, err
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(rd, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(line[1:])
		if err != nil || n < 0 {
			return []any{}, err
		}
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			v, err := readReply(rd)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	return nil, fmt.Errorf("plane: unexpected reply %q", line)
}
