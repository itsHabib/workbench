# kvlab

Build a small key-value engine as a Go module named `kvlab` (go 1.22, standard library only).
Five packages. Hidden tests exercise each package and the whole engine, so the names and
behaviour below are exact.

## store
- `func New(now func() time.Time) *Store`. `now` is the only clock the store reads.
- `(*Store) Set(key, val string, ttl time.Duration)`. A ttl of 0 never expires. Setting a key again replaces value and ttl.
- `(*Store) Get(key string) (string, bool)`. An expired key is absent. A key expires when `now()` is at or past set-time plus ttl.
- `(*Store) Delete(key string) bool`. True only if a live key was removed.
- `(*Store) Keys() []string`. Live keys, sorted ascending. `(*Store) Len() int`. Count of live keys.
- Safe for concurrent use.

## wal
- `type Entry struct { Op, Key, Val string; TTLms int64 }`. Op is `set` or `del`.
- `func Encode(e Entry) string`. One line, no newline in the output, whatever bytes the key and value hold (spaces, tabs, newlines, quotes, unicode).
- `func Decode(line string) (Entry, error)`. `Decode(Encode(e)) == e` for every Entry. Garbage is an error.
- `func Replay(r io.Reader, s *store.Store) (int, error)`. Applies each line to the store, skips blank lines, returns how many entries it applied. A bad line stops the replay with an error whose text contains `line N` (1-based).

## query
- `type Cmd struct { Op, Key, Val string; TTL time.Duration }`. Op is upper case.
- `func Parse(s string) (Cmd, error)`.
- Grammar: `SET key value [EX seconds]`, `GET key`, `DEL key`, `KEYS`, `COUNT`. The op word is case-insensitive. Extra or missing arguments are errors. So is an unknown op, an empty line, a non-integer or negative `EX`.
- A value may be double-quoted to hold spaces. Inside quotes `\"` is a quote and `\\` a backslash. An unterminated quote is an error.

## stats
- `func Percentile(xs []float64, p float64) (float64, error)`. Nearest-rank: sort ascending, rank = ceil(p/100 * n), rank 0 becomes 1. Error when xs is empty or p is outside [0,100]. Must not reorder the caller's slice.
- `type Sum struct { N int; Min, Max, Mean, P50, P95 float64 }` and `func Summary(xs []float64) Sum`. The zero Sum for empty input.

## engine
- `func New(now func() time.Time, log io.Writer) *Engine`. `log` may be nil.
- `(*Engine) Exec(line string) (string, error)`. Parses with `query`, applies to a `store`.
  Replies: SET gives `OK`; GET gives the value or `(nil)`; DEL gives `1` or `0`; KEYS gives keys joined by `\n` (empty string when none); COUNT gives the decimal count.
  Every SET, and every DEL that removed something, writes `wal.Encode(entry) + "\n"` to `log`. A parse error writes nothing.
- `(*Engine) Restore(r io.Reader) error`. Replays a log into the store using `wal.Replay`. Restoring writes nothing to `log`.
- `(*Engine) ValueSizes() stats.Sum`. `stats.Summary` over the byte lengths of live values.

Done means: `go build ./... && go vet ./... && go test ./...` pass on `main` at `origin`, and each package has tests of its own.
