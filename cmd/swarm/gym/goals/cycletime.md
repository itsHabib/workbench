# Goal: a cycle-time estimator for a machine shop

Build a command-line tool, as this Go module (standard library only), that a machinist can
use to estimate how long a part will take to machine on a 3-axis mill.

That is the whole brief. It is deliberately incomplete. Before writing code, the team must
decide and record, as swarm decisions, at least:

- what the input is (a file format, flags, an interactive prompt) and what one example input looks like
- what the output is and how a wrong estimate would be noticed
- which operations are modelled (facing, pocketing, drilling, contouring, at least) and the formula for each
- what material and tool data the tool ships with and where a user adds their own
- how the estimator is tested, given there is no ground truth in this repository

Then build it, with tests, so that `go build ./... && go vet ./... && go test ./...` pass on
origin main and a README shows a worked example from input to estimate.

Done means: the decisions above are in the ledger, the tool runs the README example, and the
suite is green on origin main.
