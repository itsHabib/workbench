#!/bin/sh
# One-command canned demo: the six common cases, in order, in a freshly
# created disposable directory that is removed afterwards (KEEP=1 keeps it).
# Every effect stays inside that directory and its child processes. Exit
# codes are checked, so the demo fails loudly if behavior regresses; the
# assertions that prove each case live in cmd/wb/*_test.go.
set -eu

repo=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/wb-demo.XXXXXX")
if [ "${KEEP:-}" = 1 ]; then echo "keeping $tmp"; else trap 'rm -rf "$tmp"' EXIT; fi
mkdir "$tmp/bin" "$tmp/ws" "$tmp/shim"
(cd "$repo" && go build -o "$tmp/bin/wb" ./cmd/wb)
cp "$repo/examples/pipeline.wb" "$tmp/pipeline.wb"
cd "$tmp"
PATH="$tmp/bin:$PATH"

say() { printf '\n## %s\n\n' "$*"; }
note() { printf '# %s\n' "$*"; }

# run CODE CMD...: show a command, run it, and require its exit code.
run() {
	want=$1
	shift
	printf '$ %s\n' "$*"
	set +e
	"$@" 2>&1
	got=$?
	set -e
	if [ "$got" != "$want" ]; then
		echo "demo: expected exit $want, got $got" >&2
		exit 1
	fi
	if [ "$got" != 0 ]; then echo "(exit $got)"; fi
}

# edit FROM TO: change the input text in the source, as a person would.
edit() {
	printf '$ sed "s/%s/%s/" pipeline.wb > next.wb && mv next.wb pipeline.wb\n' "$1" "$2"
	sed "s/$1/$2/" pipeline.wb > next.wb && mv next.wb pipeline.wb
}

say "1. Initial plan and apply in a freshly created directory"
note "source -> normalized intent: references resolved, dependency order, ownership"
run 0 wb check pipeline.wb
run 0 wb intent pipeline.wb
note "keep = a resource wb converges; run = work wb replays only when stale"
run 0 wb plan -dir ws -out plan.json pipeline.wb
run 0 wb apply -dir ws -plan plan.json
run 0 cat ws/notes.upper.txt ws/notes.count.txt

say "2. Unchanged re-apply"
note "nothing is rewritten and no command runs: every receipt still matches"
run 0 wb apply -dir ws pipeline.wb

say "3. Input change and the resulting downstream plan"
edit "brown fox" "red fox"
run 0 wb plan -dir ws -out plan.json pipeline.wb
run 0 wb apply -dir ws -plan plan.json

say "4. External modification between plan and apply"
edit "red fox" "grey fox"
run 0 wb plan -dir ws -out plan.json pipeline.wb
printf '$ echo "edited by hand" > ws/notes.txt\n'
echo "edited by hand" > ws/notes.txt
note "the saved plan was reviewed against different facts, so apply refuses it"
run 3 wb apply -dir ws -plan plan.json
run 0 cat ws/notes.txt
note "re-planning shows the hand edit and that applying overwrites it"
run 0 wb plan -dir ws -out plan.json pipeline.wb
run 0 wb apply -dir ws -plan plan.json

say "5. Injected partial failure, then retry"
note "shim/awk fails; shout (tr) is unaffected, count (awk) fails"
printf '#!/bin/sh\necho "awk: injected failure" >&2\nexit 1\n' > shim/awk
chmod +x shim/awk
edit "grey fox" "black cat"
printf '$ PATH="$PWD/shim:$PATH" wb apply -dir ws pipeline.wb\n'
set +e
PATH="$tmp/shim:$PATH" wb apply -dir ws pipeline.wb 2>&1
got=$?
set -e
[ "$got" = 1 ] || { echo "demo: expected exit 1, got $got" >&2; exit 1; }
echo "(exit $got)"
note "retry with the real awk: completed work is skipped, failed work reruns"
run 0 wb apply -dir ws pipeline.wb

say "6. A third adapter: keep dir, a disposable scratch directory"
note "the extension is adapters/dir/dir.go plus one registry entry in cmd/wb/main.go"
note "internal/lang and internal/plan are unchanged; the new kind is simply registered:"
run 0 wb kinds
printf '$ cat examples/scratch.wb >> pipeline.wb\n'
{ echo; cat "$repo/examples/scratch.wb"; } >> pipeline.wb
run 0 wb plan -dir ws -out plan.json pipeline.wb
run 0 wb apply -dir ws -plan plan.json
run 0 cat ws/words.txt
note "disposable: delete it and wb recreates it without redoing the work that used it"
run 0 rm -r ws/scratch
run 0 wb apply -dir ws pipeline.wb

say "Retained evidence"
note "the journal is what a fresh caller reads to recover; planning never writes it"
run 0 wb evidence -dir ws
