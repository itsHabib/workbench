// Package fleet is the substrate: one process per harness event, a directory of
// JSON files under $FLEET_STATE, and the policy that decides a tool call from them.
//
// It is a port of the reference hook.py, kept faithful on purpose: the record
// shapes, filenames, exit codes and refusal texts are a contract that a 316-scenario
// suite pins and that a store written by the Python must still satisfy. Where this
// file departs from the Python it says so in a comment. The laws are unchanged:
//
//	fail-open      — an internal error exits 0 with no output, except on the lease path
//	deny on evidence — a positive stop flag or a live foreign lease
//	redirect       — every denial names the next action
//	one spawn      — the hook spawns nothing; the branch is read from .git, not from git
//
// Records are map[string]any rather than structs. That is a porting decision, not a
// design one: the Python stores dynamic dicts, and giving them types while also
// changing the substrate underneath would be two changes in one. Typed contracts
// come with the ledger, not the port.
package fleet

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	// State is $FLEET_STATE, default ~/.fleet.
	State = expand(envOr("FLEET_STATE", "~/.fleet"))
	// OrgState is $ORG_STATE, default ~/dev/org/state: roles.map lives there.
	OrgState = expand(envOr("ORG_STATE", "~/dev/org/state"))
	// StaleS is how long a turn may stay open with no pid proof before it reads stale.
	StaleS = envInt("FLEET_STALE_S", 7200)
)

const slowNoteS = 30 // PostToolUse reports elapsed above this

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return d
}

func expand(p string) string {
	if strings.HasPrefix(p, "~") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[1:])
		}
	}
	return p
}

// Now is seconds since the epoch as a float, the shape every record stores.
func Now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// Path joins under the state root.
func Path(parts ...string) string { return filepath.Join(append([]string{State}, parts...)...) }

// Rec is one JSON record as the store holds it.
type Rec = map[string]any

// ReadJSON is the record at p, or nil on any failure. Callers that must tell
// "absent" from "unreadable" check os.Stat first, as lease and stopFlag do.
func ReadJSON(p string) Rec {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

// readAny is ReadJSON without the object requirement, for files that may hold a list.
func readAny(p string) any {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil
	}
	return v
}

// WriteJSON writes obj to p through a per-process temp and a rename, so a reader
// never sees a partial file. The temp name carries the pid: concurrent hooks for
// one session once shared a temp and raced on the rename.
func WriteJSON(p string, obj any) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", p, os.Getpid())
	if err := os.WriteFile(tmp, DumpJSON(obj), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// AppendJSONL appends one line.
func AppendJSONL(p string, obj any) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(DumpJSON(obj), '\n'))
	return err
}

// logError appends to hook-errors.jsonl and never fails.
func logError(fields Rec) {
	fields["at"] = Now()
	_ = AppendJSONL(Path("hook-errors.jsonl"), fields)
}

// DumpJSON encodes the way Python's json.dumps does — `", "` and `": "` separators,
// non-ASCII escaped — because the suite greps store files for `"session": "<id>"`
// and a store the Python wrote must read back byte-for-byte alike. Keys are sorted
// (Python keeps insertion order; nothing reads order, and sorted is deterministic).
func DumpJSON(v any) []byte {
	var b bytes.Buffer
	dump(&b, v)
	return b.Bytes()
}

func dump(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		dumpString(b, x)
	case float64:
		dumpFloat(b, x)
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case []string:
		b.WriteByte('[')
		for i, s := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			dumpString(b, s)
		}
		b.WriteByte(']')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			dump(b, e)
		}
		b.WriteByte(']')
	case []Rec:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			dump(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			dumpString(b, k)
			b.WriteString(": ")
			dump(b, x[k])
		}
		b.WriteByte('}')
	default:
		// Anything else goes through the standard encoder; it is never a store record.
		enc, err := json.Marshal(x)
		if err != nil {
			b.WriteString("null")
			return
		}
		b.Write(enc)
	}
}

func dumpFloat(b *bytes.Buffer, f float64) {
	switch {
	case math.IsNaN(f):
		b.WriteString("NaN")
	case math.IsInf(f, 1):
		b.WriteString("Infinity")
	case math.IsInf(f, -1):
		b.WriteString("-Infinity")
	case f == math.Trunc(f) && math.Abs(f) < 1e15:
		// A whole number is written as Python writes a float: `5.0`, never `5`.
		b.WriteString(strconv.FormatFloat(f, 'f', 1, 64))
	default:
		b.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
	}
}

func dumpString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(b, `\u%04x`, r)
		case r > 0x7e:
			if r > 0xffff {
				r -= 0x10000
				fmt.Fprintf(b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			} else {
				fmt.Fprintf(b, `\u%04x`, r)
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

var safeRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Safe is a key or name as a filename component: every other character becomes `__`.
func Safe(name string) string { return safeRe.ReplaceAllString(name, "__") }

// ---------- record accessors ----------

// S is the string at k, or "" — and "" for a non-string, which is what Python's
// f-strings would have rendered as the literal `None` but which no message wants.
func S(m Rec, k string) string {
	if m == nil {
		return ""
	}
	switch v := m[k].(type) {
	case string:
		return v
	default:
		return ""
	}
}

// F is the number at k, or 0.
func F(m Rec, k string) float64 {
	if m == nil {
		return 0
	}
	switch v := m[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return 0
	}
}

// B is the truthiness of k the way Python reads it: nil, false, 0, "" and empty
// containers are false.
func B(m Rec, k string) bool {
	if m == nil {
		return false
	}
	return truthy(m[k])
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

// Has reports whether k is present at all (Python's `"k" in d`).
func Has(m Rec, k string) bool {
	if m == nil {
		return false
	}
	_, ok := m[k]
	return ok
}

// M is the object at k, or nil.
func M(m Rec, k string) Rec {
	if m == nil {
		return nil
	}
	v, _ := m[k].(map[string]any)
	return v
}

// L is the list at k, or nil.
func L(m Rec, k string) []any {
	if m == nil {
		return nil
	}
	v, _ := m[k].([]any)
	return v
}

// Strs is the list of strings at k, dropping anything that is not a string.
func Strs(m Rec, k string) []string {
	var out []string
	for _, e := range L(m, k) {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Short is the first eight characters of a session id, as every message prints it.
func Short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// ---------- keys: one lease primitive, keyed by string (TDD §4.3) ----------
//
// A key is `repo:<repo-id>:<branch>` for a branch or `slot:<name>` for a scarce
// resource. Every per-key file lives at <dir>/<safe(key)>.json. The prefix is the
// only thing the substrate ever branches on, for one rule: what happens to a dead
// holder (§4.6).

// KeyFile is the per-key file under sub.
func KeyFile(sub, key string) string { return Path(sub, Safe(key)+".json") }

// KeyParts is {"kind":"branch","repo","branch"} or {"kind":"resource","name"}; nil
// for an unknown shape.
func KeyParts(key string) Rec {
	if strings.HasPrefix(key, "slot:") {
		return Rec{"kind": "resource", "name": key[len("slot:"):]}
	}
	if strings.HasPrefix(key, "repo:") {
		rest := key[len("repo:"):]
		repo, branch, _ := strings.Cut(rest, ":")
		return Rec{"kind": "branch", "repo": repo, "branch": branch}
	}
	return nil
}

// KeyLabel is how a key is named in a message: the branch for a branch key, the key
// itself for a slot.
func KeyLabel(key string) string {
	if b := S(KeyParts(key), "branch"); b != "" {
		return b
	}
	return key
}

// BranchKey is `repo:<repo-id>:<branch>` for the checkout containing start, or "".
func BranchKey(start, branch string) string {
	rid := RepoID(start)
	if rid == "" || branch == "" {
		return ""
	}
	return "repo:" + rid + ":" + branch
}

// Scope is BranchKey by its policy name: the key for per-branch state. A branch
// name is not unique on a machine — `main` in two repos was one lease file.
func Scope(start, branch string) string { return BranchKey(start, branch) }

// IsResource reports a `slot:` key.
func IsResource(key string) bool { return S(KeyParts(key), "kind") == "resource" }

// FmtAge renders seconds the way every message does: 45s, 12m, 3h07m, 2d04h.
func FmtAge(seconds float64) string {
	s := int(math.Max(0, seconds))
	switch {
	case s < 90:
		return fmt.Sprintf("%ds", s)
	case s < 5400:
		return fmt.Sprintf("%dm", s/60)
	case s < 48*3600:
		return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
	default:
		return fmt.Sprintf("%dd%02dh", s/86400, (s%86400)/3600)
	}
}

func sha1hex(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// Unlink removes p and never fails.
func Unlink(p string) { _ = os.Remove(p) }

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// listDir is the sorted entry names of d, or nil when it is not a directory.
func listDir(d string) []string {
	ents, err := os.ReadDir(d)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// readText is the file's contents, or "" and false.
func readText(p string) (string, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(b), true
}
