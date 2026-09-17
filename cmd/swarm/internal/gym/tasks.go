// Package gym measures which model is good enough for which seat. A task
// is a small repository fixture, a prompt for one seat, and a grader the
// model never sees. The grader is a command with an exit code: hidden tests
// for an author, the ledger's recorded order for a ruler, a verified merged
// head for a consolidator. Nothing is graded by reading prose.
package gym

// Task is one graded exercise for one kind of seat.
type Task struct {
	ID     string `json:"id"`
	Seat   string `json:"seat"`  // author | ruler | consolidator
	Level  string `json:"level"` // easy | mid | hard: a gradient, so a table can separate models
	Goal   string `json:"goal"`  // the whole prompt the seat gets, besides the standing rules
	Turns  int    `json:"max_turns"`
	WallS  int    `json:"wall_s"`
	Grader string `json:"grader"` // what decides pass, in words; the code is in fixtures.go
}

// Tasks is the gym. IDs are stable: results are keyed by them.
func Tasks() []Task {
	return []Task{
		{ID: "author-intervals", Seat: "author", Level: "easy", Turns: 30, WallS: 420,
			Goal:   "Implement Merge in pkg/intervals/intervals.go exactly as its doc comment specifies. Add your own tests. Run `go test ./...` until it passes, then commit your work.",
			Grader: "hidden table tests: unsorted input, touching intervals, containment, empty and single, input not mutated"},
		{ID: "author-lru", Seat: "author", Level: "mid", Turns: 40, WallS: 480,
			Goal:   "Implement the LRU cache in pkg/lru/lru.go exactly as its doc comments specify. Add your own tests. Run `go test ./...` until it passes, then commit your work.",
			Grader: "hidden tests: recency on Get, update does not grow, eviction order, zero capacity, Len"},
		{ID: "author-toposort", Seat: "author", Level: "mid", Turns: 40, WallS: 480,
			Goal:   "Implement Order in pkg/toposort/toposort.go exactly as its doc comment specifies. Add your own tests. Run `go test ./...` until it passes, then commit your work.",
			Grader: "hidden tests: lexicographically smallest valid order, dependencies that are not keys, cycle reported as an error naming a node on it, determinism"},
		{ID: "author-bucket", Seat: "author", Level: "hard", Turns: 50, WallS: 600,
			Goal:   "Implement the token bucket in pkg/bucket/bucket.go exactly as its doc comments specify. It takes its clock as a parameter; never call time.Now. Add your own tests. Run `go test ./...` until it passes, then commit your work.",
			Grader: "hidden tests with an injected clock: burst then refusal, fractional refill accumulates, never above capacity after a long gap, clock going backwards ignored, AllowN atomicity"},
		{ID: "ruler-order", Seat: "ruler", Level: "mid", Turns: 25, WallS: 300,
			Goal:   "You are seat `helper`, a peer in a swarm. Run `swarm requests`. One request is open. Answer it from evidence: read the two branches involved (`git log`, `git diff main...<branch>`), decide which must land first and why, and record the ruling with `swarm rule <id> --order \"<first>,<second>\" --ruling \"...\" --evidence \"...\"`. If it is a product decision the brief (briefs/BRIEF.md) does not settle, escalate it instead with `swarm escalate <id> --to operator --why \"...\"`. One shell command per tool call.",
			Grader: "the ledger's effective ruling records order [feat-parse, feat-report]: feat-report calls a function only feat-parse adds"},
		{ID: "ruler-escalate", Seat: "ruler", Level: "mid", Turns: 25, WallS: 300,
			Goal:   "You are seat `helper`, a peer in a swarm. Run `swarm requests`. One request is open. Answer it from evidence: read the two branches involved (`git log`, `git diff main...<branch>`), decide which must land first and why, and record the ruling with `swarm rule <id> --order \"<first>,<second>\" --ruling \"...\" --evidence \"...\"`. If it is a product decision the brief (briefs/BRIEF.md) does not settle, escalate it instead with `swarm escalate <id> --to operator --why \"...\"`. One shell command per tool call.",
			Grader: "the request ends needing the operator and no peer ruling exists: the question is which export format customers want, which the brief does not say"},
		{ID: "consolidate-three", Seat: "consolidator", Level: "mid", Turns: 60, WallS: 600,
			Goal:   "You are seat `theme`, the consolidator, on branch theme. Run `swarm consolidate --verify \"go test ./...\"`. It merges what merges cleanly and lists the branches that CONFLICTED. For each of those, in the order listed: `git merge --no-ff <branch> -m \"merge <branch>\"`, resolve the conflict so every branch's behavior survives (every key, every test), run `go test ./...`, commit. Then run `git rev-parse HEAD`, write briefs/out/theme/RESULT.json as {\"head_sha\": \"<that sha>\", \"claims\": [<merged branches>]}, commit only that file, and stop. One shell command per tool call.",
			Grader: "theme is landed (RESULT pins its head), contains all three branches, and a hidden test asserting every key passes at that head"},
	}
}
