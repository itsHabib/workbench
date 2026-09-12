#!/bin/sh
# The lease protocol, bounded and machine-checked. Needs quint 0.32, jq, java, Go.
#   ./judge.sh
# Exhausts the reference for both key kinds (TLC), then reproduces four
# counterexamples byte-for-byte against the frozen traces (Apalache):
# unreadable-is-dead (the review's finding), silent resource takeover, and a
# check-then-write with no lock, and crash/replacement with a surviving child.
# Also checks independent parent/child lifetimes
# and replays the crash/replacement counterexample against Go on Unix.
set -eu
cd "$(dirname "$0")"
export PATH="/opt/homebrew/opt/openjdk/bin:$PATH"

for tool in quint jq cmp mktemp go java; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required local tool: $tool" >&2; exit 2; }
done
case "$(quint --version)" in
  0.32.*) ;;
  *) echo "Quint 0.32.x required" >&2; exit 2 ;;
esac

work_dir="$(mktemp -d /tmp/fleet-lease-model.XXXXXX)"
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

for source in model/common.qnt model/reference.qnt model/mutant_unreadable_is_dead.qnt model/mutant_silent_resource_takeover.qnt model/mutant_unlocked_check.qnt model/crash_replacement.qnt; do
  quint typecheck "$source" >"$work_dir/typecheck.log" 2>&1
done
echo "PASS typecheck: all Quint modules"

for main in CrashResource QuiescentBranch; do
  # resourceRetained applies only to the resource variant; noStaleChildWrite
  # is the substantive safety property for both variants.
  quint verify model/crash_replacement.qnt \
    --main="$main" --invariants=noStaleChildWrite resourceRetained \
    --backend=tlc --max-steps=12 --verbosity=0 >"$work_dir/$main.log" 2>&1
  echo "PASS $main: no stale child write in the finite model (12-step configuration)"
done

for main in ReferenceBranch ReferenceResource; do
  quint verify model/reference.qnt \
    --main="$main" \
    --invariants=exclusion writerHolds evidenceNotDeath noSilentResourceTakeover \
    --backend=tlc \
    --max-steps=12 \
    --verbosity=0 >"$work_dir/$main.log" 2>&1
  echo "PASS $main: complete finite-state exploration to 12 steps found no violation"
done

expect_counterexample() {
  name="$1"; source="$2"; module="$3"; invariant="$4"; steps="$5"; frozen="$6"
  model_step="${7:-step}"
  set +e
  quint verify "$source" \
    --main="$module" \
    --step="$model_step" \
    --invariant="$invariant" \
    --max-steps="$steps" \
    --out-itf="$work_dir/$name.itf.json" \
    --verbosity=0 >"$work_dir/$name.log" 2>&1
  status=$?
  set -e
  test "$status" -eq 1 || {
    echo "$name: expected checker counterexample, got exit $status" >&2
    sed -n '1,80p' "$work_dir/$name.log" >&2
    exit 1
  }
  grep -q 'found a counterexample' "$work_dir/$name.log"
  jq -e '.states | length > 1' "$work_dir/$name.itf.json" >/dev/null
  jq -S -f scripts/normalize-itf.jq "$work_dir/$name.itf.json" >"$work_dir/$name.trace.json"
  cmp "$frozen" "$work_dir/$name.trace.json"
  echo "PASS $name: counterexample reproduced byte-for-byte"
}

expect_counterexample unreadable-is-dead model/mutant_unreadable_is_dead.qnt UnreadableIsDeadMutantBranch evidenceNotDeath 6 artifacts/unreadable-is-dead.trace.json
expect_counterexample silent-resource-takeover model/mutant_silent_resource_takeover.qnt SilentResourceTakeoverMutantResource noSilentResourceTakeover 6 artifacts/silent-resource-takeover.trace.json
expect_counterexample unlocked-check model/mutant_unlocked_check.qnt UnlockedCheckMutantBranch exclusion 6 artifacts/unlocked-check.trace.json
expect_counterexample crash-replacement model/crash_replacement.qnt CrashBranch noConflictingEffects 6 artifacts/crash-replacement.trace.json witnessStep

# Frozen traces must retain the salient failures, not merely parse as JSON.
jq -e 'last | (.inFlightA or .inFlightB) and ((.liveA == "Unreadable") or (.liveB == "Unreadable"))' artifacts/unreadable-is-dead.trace.json >/dev/null
jq -e 'last.facts | index("SilentA") or index("SilentB")' artifacts/silent-resource-takeover.trace.json >/dev/null
jq -e 'last | .inFlightA and .inFlightB' artifacts/unlocked-check.trace.json >/dev/null
jq -e 'last | .owner == "B" and (.parentAlive | not) and .childAlive and .staleWrite and .replacementWrote' artifacts/crash-replacement.trace.json >/dev/null

if [ "$(go env GOOS)" = windows ]; then
  echo "SKIP crash/replacement process replay: Unix-only; no Windows runtime claim"
else
  go test ../internal/fleet -run '^TestCrashReplacementModelTrace$' -count=1 -v
  echo "PASS crash/replacement replay: branch limitation reproduced; resource refused"
fi

for required in README.md SOURCE_MAP.md CLAIMS.md; do
  test -s "$required" || { echo "missing required artifact: $required" >&2; exit 1; }
done
echo "PASS artifacts: frozen failures and required ledgers are present"
echo "ALL CHECKS PASS"
