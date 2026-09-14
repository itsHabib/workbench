# Try a receipt-to-assignment handoff

From the Workbench root:

```sh
python3 cmd/fleet/examples/relay-boundary/demo.py
```

Requires Go, Git and Python 3 (standard library only). The script builds Fleet in
a fresh temporary directory, creates a tiny Go repository and a second checkout,
and prints the retained evidence directory. It overrides Fleet/Org state and
disables GitHub and the watcher. It uses synthetic harness events to represent
two participants: no model calls, agent launches, paid hosts or global installation.

The example exercises actual CLI processes:

1. A receiving assignment refuses without a required receipt.
2. `TestTwice` fails on the first commit; that failure receipt refuses admission.
3. Fixing the code creates another commit. The old revision and missing new evidence
   both refuse. The test then passes and produces a receipt at the new revision.
4. Deliberately conflicting publication/history refuses without an assignment.
   Restoring the fixture's original bytes permits admission.
5. The assignment retains its input evidence. Restarting the CLI or replaying through
   MCP preserves the same bytes; changing the input under that ID refuses.
6. A separate checkout consumes the pinned commit, runs the check and records its
   own `verify` receipt. `fleet done` reads that evidence.
7. A later commit makes `fleet status` report input drift while retaining the admission.

Open `summary.json`, `commands.json`, `admission.json` and the three test logs in
the printed directory. Fleet's receipt histories and retained assignment are under
`fleet-state/`. The summary records the Workbench source revision and whether the
build came from a dirty tree. Run from a clean committed checkout for exact-head
evidence. The command log records actual outputs, including expected failures.

This proves the local contract mechanics, not review independence, adequate test
coverage, improved agent throughput, remote PR freshness or merge permission.
The receiver is a script in a separate checkout, not an independently reasoning
agent. Gate and the operator's grant remain the release boundary.

The [design](../../../../docs/features/relay-boundary/spec.md) explains why existing
primitives plus this receiving check suffice, what the brief got wrong about the
current tools, and what would justify a wider implementation.
