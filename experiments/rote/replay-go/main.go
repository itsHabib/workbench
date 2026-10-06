// Command replay-go is a conformance replayer for Rote witnesses. It
// re-implements the replay kernel (rote/witness.py) and the 21-form symbolic
// evaluator (rote/sym.py) in Go and checks them against fixtures exported by
// the Python reference: each case walks a witness against the world answers
// Python recorded, and the outcome must match. It is not a Rote
// implementation: there is no world and no `use` dispatch; every answer, and
// the acts of every nested capability, come from the fixture.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"maps"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Values are JSON as Python sees them: nil, bool, int64, string, []any and
// *object. Objects remember insertion order because `keys` and `str` expose it.
type object struct {
	keys []string
	vals map[string]any
}

func newObject() *object { return &object{vals: map[string]any{}} }

func (o *object) set(key string, v any) {
	if _, dup := o.vals[key]; !dup {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

// lookup is Python's `key in obj`: a non-string key is never present.
func (o *object) lookup(key any) (any, bool) {
	k, ok := key.(string)
	if !ok {
		return nil, false
	}
	v, ok := o.vals[k]
	return v, ok
}

// value decodes any JSON through the ordered reader, so encoding/json can fill
// the typed library and fixture structs while Rote values stay exact.
type value struct{ v any }

func (val *value) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := readValue(dec)
	val.v = v
	return err
}

func readValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '[' {
			return readList(dec)
		}
		return readObject(dec)
	case json.Number:
		n, err := strconv.ParseInt(string(t), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s is not an int: Rote values are JSON ints", t)
		}
		return n, nil
	}
	return tok, nil
}

func readList(dec *json.Decoder) (any, error) {
	items := []any{}
	for dec.More() {
		v, err := readValue(dec)
		if err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	_, err := dec.Token() // the closing bracket
	return items, err
}

func readObject(dec *json.Decoder) (any, error) {
	obj := newObject()
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, err
		}
		v, err := readValue(dec)
		if err != nil {
			return nil, err
		}
		k, _ := key.(string)
		obj.set(k, v)
	}
	_, err := dec.Token() // the closing brace
	return obj, err
}

func list(v any) []any {
	items, _ := v.([]any)
	return items
}

// equal is deep JSON equality. Unlike Python's ==, a bool never equals an int;
// objects compare by content regardless of key order, as dicts do.
func equal(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case int64:
		y, ok := b.(int64)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		return ok && slices.EqualFunc(x, y, equal)
	case *object:
		y, ok := b.(*object)
		return ok && maps.EqualFunc(x.vals, y.vals, equal)
	}
	return false
}

// render is Python's json.dumps(v, separators=(",", ":")): compact, keys in
// insertion order, everything outside printable ASCII escaped.
func render(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case string:
		return quote(x)
	case []any:
		return "[" + joinRendered(x, render) + "]"
	case *object:
		return "{" + joinRendered(x.keys, func(k string) string { return quote(k) + ":" + render(x.vals[k]) }) + "}"
	}
	return fmt.Sprintf("<%T>", v)
}

func joinRendered[T any](items []T, one func(T) string) string {
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = one(item)
	}
	return strings.Join(parts, ",")
}

var escapes = map[rune]string{'"': `\"`, '\\': `\\`, '\n': `\n`, '\r': `\r`, '\t': `\t`, '\b': `\b`, '\f': `\f`}

// quote escapes as json.dumps does with ensure_ascii: printable ASCII passes
// through, anything else becomes \uXXXX, a surrogate pair above the BMP.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		b.WriteString(escape(r))
	}
	b.WriteByte('"')
	return b.String()
}

func escape(r rune) string {
	if esc, ok := escapes[r]; ok {
		return esc
	}
	if r >= ' ' && r <= '~' {
		return string(r)
	}
	if r > 0xFFFF {
		hi, lo := utf16.EncodeRune(r)
		return fmt.Sprintf(`\u%04x\u%04x`, hi, lo)
	}
	return fmt.Sprintf(`\u%04x`, r)
}

// toStr is sym.py's _to_str.
func toStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "unit"
	case string:
		return x
	}
	return render(v)
}

type env struct {
	params map[string]any
	refs   map[int64]any
}

func (en *env) param(name any) (any, error) {
	n, ok := name.(string)
	v, bound := en.params[n]
	if !ok || !bound {
		return nil, fmt.Errorf("unbound parameter %v", name)
	}
	return v, nil
}

func (en *env) ref(id any) (any, error) {
	n, ok := id.(int64)
	v, bound := en.refs[n]
	if !ok || !bound {
		return nil, fmt.Errorf("step %v has not produced a value", id)
	}
	return v, nil
}

// arity is the length of each of the 21 forms, tag included.
var arity = map[string]int{
	"const": 2, "param": 2, "ref": 2, "field": 3, "index": 3, "len": 2,
	"has": 3, "get": 4, "keys": 2, "contains": 3, "str": 2, "not": 2, "neg": 2,
	"min": 3, "max": 3, "bin": 4, "list": 2, "record": 2,
	"sum": 2, "maxof": 3, "minof": 3,
}

// ops are the forms whose operands are all evaluated first, left to right.
var ops = map[string]func([]any) (any, error){
	"field": opField, "index": opIndex, "len": opLen, "has": opHas, "keys": opKeys,
	"contains": opContains, "str": opStr, "not": opNot, "neg": opNeg, "min": opMin, "max": opMax,
	"sum": opSum, "maxof": opMaxOf, "minof": opMinOf,
}

// shape checks an expression's outline: a list headed by a known tag with the
// right number of operands.
func shape(e any) ([]any, string, error) {
	form, ok := e.([]any)
	if !ok || len(form) == 0 {
		return nil, "", errors.New("expression is not a tagged list")
	}
	tag, _ := form[0].(string)
	n, known := arity[tag]
	if !known {
		return nil, "", fmt.Errorf("unknown expression tag %q", tag)
	}
	if len(form) != n {
		return nil, "", fmt.Errorf("%s takes %d operands, got %d", tag, n-1, len(form)-1)
	}
	return form, tag, nil
}

// eval is sym.py's eval_sexpr; an error here is a SymError there.
func eval(e any, en *env) (any, error) {
	form, tag, err := shape(e)
	if err != nil {
		return nil, err
	}
	switch tag {
	case "const":
		return form[1], nil
	case "param":
		return en.param(form[1])
	case "ref":
		return en.ref(form[1])
	case "get":
		return evalGet(form, en)
	case "bin":
		return evalBin(form, en)
	case "list":
		return evalAll(list(form[1]), en)
	case "record":
		return evalRecord(list(form[1]), en)
	}
	args, err := evalAll(form[1:], en)
	if err != nil {
		return nil, err
	}
	return ops[tag](args)
}

func evalAll(forms []any, en *env) ([]any, error) {
	out := make([]any, 0, len(forms))
	for _, f := range forms {
		v, err := eval(f, en)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func evalRecord(pairs []any, en *env) (any, error) {
	obj := newObject()
	for _, p := range pairs {
		pair := list(p)
		if len(pair) != 2 {
			return nil, errors.New("record entries are [key, expr] pairs")
		}
		key, _ := pair[0].(string)
		v, err := eval(pair[1], en)
		if err != nil {
			return nil, err
		}
		obj.set(key, v)
	}
	return obj, nil
}

// evalGet evaluates the default lazily, only when the key is absent.
func evalGet(form []any, en *env) (any, error) {
	obj, err := eval(form[1], en)
	if err != nil {
		return nil, err
	}
	key, err := eval(form[2], en)
	if err != nil {
		return nil, err
	}
	if _, err := stringKey("get", key); err != nil {
		return nil, err
	}
	if o, ok := obj.(*object); ok {
		if v, found := o.lookup(key); found {
			return v, nil
		}
	}
	return eval(form[3], en)
}

func evalBin(form []any, en *env) (any, error) {
	op, _ := form[1].(string)
	a, err := eval(form[2], en)
	if err != nil {
		return nil, err
	}
	b, err := eval(form[3], en)
	if err != nil {
		return nil, err
	}
	switch op {
	case "==":
		return equal(a, b), nil
	case "!=":
		return !equal(a, b), nil
	}
	sa, aStr := a.(string)
	sb, bStr := b.(string)
	if op == "+" && aStr && bStr {
		return sa + sb, nil
	}
	x, y, err := twoInts(op, a, b)
	if err != nil {
		return nil, err
	}
	return arith(op, x, y)
}

// twoInts is sym.py's _typecheck_bin for everything but equality and string
// concatenation: both operands must be ints, and a bool is not an int.
func twoInts(op string, a, b any) (int64, int64, error) {
	x, okA := a.(int64)
	y, okB := b.(int64)
	if !okA || !okB {
		return 0, 0, fmt.Errorf("operator %s needs two ints, got %T and %T", op, a, b)
	}
	return x, y, nil
}

// checkedArith is sym.py's `ranged`: Rote ints are signed 64-bit and leaving the
// range is an error in every kernel, never a silent wrap.
func checkedArith(op string, x, y int64) (int64, error) {
	var r int64
	switch op {
	case "+":
		r = x + y
		if (y > 0 && r < x) || (y < 0 && r > x) {
			return 0, errors.New("integer overflow: result is outside the signed 64-bit range")
		}
	case "-":
		r = x - y
		if (y < 0 && r < x) || (y > 0 && r > x) {
			return 0, errors.New("integer overflow: result is outside the signed 64-bit range")
		}
	case "*":
		r = x * y
		if x != 0 && (r/x != y || (x == -1 && y == math.MinInt64)) {
			return 0, errors.New("integer overflow: result is outside the signed 64-bit range")
		}
	}
	return r, nil
}

func arith(op string, x, y int64) (any, error) {
	switch op {
	case "+", "-", "*":
		return checkedArith(op, x, y)
	case "/", "%":
		if op == "/" && x == math.MinInt64 && y == -1 {
			return nil, errors.New("integer overflow: result is outside the signed 64-bit range")
		}
		return divmod(op, x, y)
	case "<":
		return x < y, nil
	case "<=":
		return x <= y, nil
	case ">":
		return x > y, nil
	case ">=":
		return x >= y, nil
	}
	return nil, fmt.Errorf("unknown operator %q", op)
}

// divmod is Python's // and %: the quotient rounds toward negative infinity
// and the remainder takes the sign of the divisor.
func divmod(op string, x, y int64) (any, error) {
	if y == 0 {
		return nil, errors.New("division by zero")
	}
	q, r := x/y, x%y
	if r != 0 && (r < 0) != (y < 0) {
		q, r = q-1, r+y
	}
	if op == "/" {
		return q, nil
	}
	return r, nil
}

func stringKey(what string, k any) (string, error) {
	key, ok := k.(string)
	if !ok {
		return "", fmt.Errorf("%s needs a string key, got %T", what, k)
	}
	return key, nil
}

func opField(a []any) (any, error) {
	obj, ok := a[0].(*object)
	if !ok {
		return nil, fmt.Errorf("field access on %T", a[0])
	}
	if _, err := stringKey("field", a[1]); err != nil {
		return nil, err
	}
	v, ok := obj.lookup(a[1])
	if !ok {
		return nil, fmt.Errorf("missing field %s", render(a[1]))
	}
	return v, nil
}

func opIndex(a []any) (any, error) {
	seq, isList := a[0].([]any)
	i, isInt := a[1].(int64)
	if !isList || !isInt {
		return nil, errors.New("index needs a list and an int")
	}
	if i < 0 || i >= int64(len(seq)) {
		return nil, fmt.Errorf("index %d out of range for length %d", i, len(seq))
	}
	return seq[i], nil
}

func opLen(a []any) (any, error) {
	switch x := a[0].(type) {
	case []any:
		return int64(len(x)), nil
	case *object:
		return int64(len(x.vals)), nil
	case string:
		return int64(utf8.RuneCountInString(x)), nil
	}
	return nil, fmt.Errorf("len of %T", a[0])
}

func opHas(a []any) (any, error) {
	if _, err := stringKey("has", a[1]); err != nil {
		return nil, err
	}
	obj, ok := a[0].(*object)
	if !ok {
		return false, nil
	}
	_, found := obj.lookup(a[1])
	return found, nil
}

func opKeys(a []any) (any, error) {
	obj, ok := a[0].(*object)
	if !ok {
		return nil, fmt.Errorf("keys of %T", a[0])
	}
	keys := make([]any, len(obj.keys))
	for i, k := range obj.keys {
		keys[i] = k
	}
	return keys, nil
}

// opContains is Python's `in`: membership for a list, substring for a string.
func opContains(a []any) (any, error) {
	switch x := a[0].(type) {
	case []any:
		return slices.ContainsFunc(x, func(v any) bool { return equal(v, a[1]) }), nil
	case string:
		needle, ok := a[1].(string)
		if !ok {
			return nil, errors.New("contains on a string needs a string")
		}
		return strings.Contains(x, needle), nil
	}
	return nil, fmt.Errorf("contains on %T", a[0])
}

func opStr(a []any) (any, error) { return toStr(a[0]), nil }

func opNot(a []any) (any, error) {
	b, ok := a[0].(bool)
	if !ok {
		return nil, errors.New("not needs a bool")
	}
	return !b, nil
}

func opNeg(a []any) (any, error) {
	n, ok := a[0].(int64)
	if !ok {
		return nil, errors.New("negation needs an int")
	}
	if n == math.MinInt64 {
		return nil, errors.New("integer overflow: result is outside the signed 64-bit range")
	}
	return -n, nil
}

// intList is sym.py's _reduce precondition: a list whose every element is an int.
func intList(tag string, v any) ([]int64, error) {
	xs, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s needs a list of ints", tag)
	}
	out := make([]int64, 0, len(xs))
	for _, x := range xs {
		n, isInt := x.(int64)
		if !isInt {
			return nil, fmt.Errorf("%s needs a list of ints", tag)
		}
		out = append(out, n)
	}
	return out, nil
}

func opSum(a []any) (any, error) {
	xs, err := intList("sum", a[0])
	if err != nil {
		return nil, err
	}
	var total int64
	for _, x := range xs {
		next, err := checkedArith("+", total, x)
		if err != nil {
			return nil, err
		}
		total = next
	}
	return total, nil
}

func opMaxOf(a []any) (any, error) { return reduceOf("maxof", a, false) }

func opMinOf(a []any) (any, error) { return reduceOf("minof", a, true) }

// reduceOf returns the default for an empty list, else the extremum.
func reduceOf(tag string, a []any, low bool) (any, error) {
	xs, err := intList(tag, a[0])
	if err != nil {
		return nil, err
	}
	def, ok := a[1].(int64)
	if !ok {
		return nil, fmt.Errorf("%s needs an int default", tag)
	}
	if len(xs) == 0 {
		return def, nil
	}
	best := xs[0]
	for _, x := range xs[1:] {
		if (low && x < best) || (!low && x > best) {
			best = x
		}
	}
	return best, nil
}

func opMin(a []any) (any, error) { return extremum(a, true) }

func opMax(a []any) (any, error) { return extremum(a, false) }

func extremum(a []any, low bool) (any, error) {
	x, y, err := twoInts("<", a[0], a[1])
	if err != nil {
		return nil, err
	}
	if low {
		return min(x, y), nil
	}
	return max(x, y), nil
}

type witness struct {
	Hash   string   `json:"hash"`
	Params []string `json:"params"`
	Steps  []step   `json:"steps"`
}

// step is one witness step: observe/act/use carry a name and symbolic args;
// a guard carries a predicate and the value it must evaluate to.
type step struct {
	Op     string `json:"op"`
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Args   value  `json:"args"`
	Pred   value  `json:"pred"`
	Expect value  `json:"expect"`
	Expr   value  `json:"expr"`
}

type fixture struct {
	Witness  string           `json:"witness"`
	Args     map[string]value `json:"args"`
	Answers  []answer         `json:"answers"`
	Expected outcome          `json:"expected"`
}

// answer is what the world said to one observe/act/use step, in execution
// order; a use answer also carries the acts the nested capability performed.
type answer struct {
	Op    string `json:"op"`
	Value value  `json:"value"`
	Acts  value  `json:"acts"`
}

type outcome struct {
	Kind string `json:"kind"`
	Step int64  `json:"step"`
	Acts value  `json:"acts"`
}

func bind(w *witness, fx *fixture) (*env, error) {
	en := &env{params: map[string]any{}, refs: map[int64]any{}}
	for _, name := range w.Params {
		v, ok := fx.Args[name]
		if !ok {
			return nil, fmt.Errorf("no argument for parameter %q", name)
		}
		en.params[name] = v.v
	}
	return en, nil
}

// replay is witness.py's kernel with the world replaced by the fixture's
// recorded answers. A guard that fails or cannot be evaluated, or a step whose
// args cannot be evaluated, is a side exit there and the acts so far stand. An
// answer that is missing or of the wrong op is a fixture violation, not an outcome.
func replay(w *witness, fx *fixture) (outcome, error) {
	en, err := bind(w, fx)
	if err != nil {
		return outcome{}, err
	}
	acts := []any{}
	answers := fx.Answers
	for i := range w.Steps {
		s := &w.Steps[i]
		if s.Op == "guard" && !holds(s, en) {
			return outcome{"side_exit", int64(i), value{acts}}, nil
		}
		if s.Op == "eval" {
			if _, err := eval(s.Expr.v, en); err != nil {
				return outcome{"side_exit", int64(i), value{acts}}, nil
			}
			continue
		}
		if s.Op == "guard" {
			continue
		}
		args, err := evalAll(list(s.Args.v), en)
		if err != nil {
			return outcome{"side_exit", int64(i), value{acts}}, nil
		}
		if len(answers) == 0 || answers[0].Op != s.Op {
			return outcome{}, fmt.Errorf("step %d (%s) has no matching answer", i, s.Op)
		}
		ans := &answers[0]
		answers = answers[1:]
		en.refs[s.ID] = ans.Value.v
		acts = append(acts, performed(s, args, ans)...)
	}
	return outcome{"completed", -1, value{acts}}, nil
}

// holds evaluates a guard; an evaluation error fails it, as Python's except
// clause turns the SymError into the same side exit.
func holds(s *step, en *env) bool {
	got, err := eval(s.Pred.v, en)
	return err == nil && equal(got, s.Expect.v)
}

// performed is what a step adds to the acts: an act adds itself, a use adds
// whatever the nested capability did as the fixture recorded it, an observe nothing.
func performed(s *step, args []any, ans *answer) []any {
	switch s.Op {
	case "act":
		return []any{[]any{s.Name, args}}
	case "use":
		return list(ans.Acts.v)
	}
	return nil
}

// check replays one fixture and describes every way its outcome differs from
// the Python kernel's; an empty string means it conforms.
func check(witnesses map[string]*witness, fx *fixture) string {
	w, ok := witnesses[fx.Witness]
	if !ok {
		return "witness not in library"
	}
	got, err := replay(w, fx)
	if err != nil {
		return "fixture violation: " + err.Error()
	}
	return diff(got, fx.Expected)
}

func diff(got, want outcome) string {
	var parts []string
	if got.Kind != want.Kind {
		parts = append(parts, fmt.Sprintf("kind %s, expected %s", got.Kind, want.Kind))
	}
	if got.Step != want.Step {
		parts = append(parts, fmt.Sprintf("step %d, expected %d", got.Step, want.Step))
	}
	if !equal(got.Acts.v, want.Acts.v) {
		parts = append(parts, fmt.Sprintf("acts %s, expected %s", render(got.Acts.v), render(want.Acts.v)))
	}
	return strings.Join(parts, "; ")
}

func load(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func main() {
	libPath := flag.String("lib", "runs/library.json", "witness library written by the Python reference")
	fxPath := flag.String("fixtures", "runs/replay_fixtures.json", "conformance fixtures from export_fixtures.py")
	flag.Parse()
	log.SetFlags(0)
	var lib struct{ Witnesses []witness }
	var cases []fixture
	if err := errors.Join(load(*libPath, &lib), load(*fxPath, &cases)); err != nil {
		log.Fatal("replay-go: ", err)
	}
	witnesses := map[string]*witness{}
	for i := range lib.Witnesses {
		witnesses[lib.Witnesses[i].Hash] = &lib.Witnesses[i]
	}
	failed := 0
	for i := range cases {
		d := check(witnesses, &cases[i])
		if d == "" {
			continue
		}
		failed++
		fmt.Printf("FAIL %s case %d: %s\n", cases[i].Witness, i, d)
	}
	fmt.Printf("%d cases, %d conform\n", len(cases), len(cases)-failed)
	if failed > 0 {
		os.Exit(1)
	}
}
