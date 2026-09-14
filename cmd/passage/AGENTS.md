# Passage

Read README.md, docs/DESIGN.md and ../../docs/features/passage/spec.md first.
This is design plus a light local POC, not an adopted workflow engine.

Preserve the inherited Fleet POC. Keep Passage state and decision code private;
compose with other tools through artifacts, never their internal packages.
No phase receipt grants merge authority, starts a worker or proves execution.
Preserve previous events and evidence when repairing or reopening work.

Checks: go test -race ./cmd/passage/..., go vet ./cmd/passage/...,
golangci-lint run ./cmd/passage/..., and python3 cmd/passage/examples/demo.py.
Exit 0 success, 1 refused/unready/runtime error, 2 argument parsing error.
No agent panel or cloud resources for this POC under the current user scope.
