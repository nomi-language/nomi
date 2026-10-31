#!/usr/bin/env bash
# check-full.sh: the full check a batch passes before `make publish-main`
# squashes next to main (AGENTS.md, "Verification cadence").
#
# Steps, in three phases. First, in order: gofmt over the tracked Go files,
# build and vet, the tour wasm bundle (vmhost's TestTourWasm* read it).
# Then, at the same time: every package but internal/irbuild, vmhost and rt
# (TestLibModules apart), vmhost alone, rt, internal/irbuild alone,
# TestLibModules and the tree-sitter tests (when npx is on PATH); the
# package step is the longest, so the others finish inside it. Then the race
# detector, its parts at the same time: rt and the language server's
# concurrent tests whole or filtered, internal/vm's and vmhost's task,
# channel, cancellation and synctest tests, and a race-built `nomi test`
# over the corpus directories whose programs run tasks. Last, every fuzz
# target at the same time for FUZZTIME, two workers each. Every step runs
# even when an earlier one fails; each prints its output, its exit status
# and its time, and the script exits 1 when any step failed.
#
# Usage:
#   scripts/check-full.sh
#
#   FUZZTIME=5m       how long each fuzz target runs (default 5m)
#   STEPS="a b ..."   run only the named steps: gofmt tourwasm buildvet pkgs
#                     vmhost rt irbuild lib treesitter, race-<part> (race-rt
#                     race-lsp race-vm race-vmhost race-corpus) and
#                     fuzz-<Target> (fuzz-FuzzFormatKeepsMeaning); `race`
#                     and `fuzz` name every part. Empty runs every step.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FUZZTIME="${FUZZTIME:-5m}"
STEPS="${STEPS:-}"
T0=$SECONDS
failed=()

FUZZ_VMHOST="FuzzFrontEndAcceptsSoItLowers FuzzFormatKeepsMeaning FuzzLoweredProgramsRun"
KNOWN="gofmt tourwasm buildvet pkgs vmhost rt irbuild lib treesitter race race-rt race-lsp race-vm race-vmhost race-corpus fuzz"
for t in $FUZZ_VMHOST FuzzIncrementalMatchesFull FuzzLSPSurvivesEdits; do
  KNOWN="$KNOWN fuzz-$t"
done
for name in $STEPS; do
  if [[ " $KNOWN " != *" $name "* ]]; then
    echo "check-full: no step named $name; the steps are: $KNOWN" >&2
    exit 2
  fi
done

# selected NAME: STEPS is empty, names NAME, or names NAME's group (`race`
# for race-vm, `fuzz` for fuzz-FuzzX).
selected() {
  [[ -z "$STEPS" || " $STEPS " == *" $1 "* || " $STEPS " == *" ${1%%-*} "* ]]
}

# step NAME CMD...: run CMD now when NAME is selected.
step() {
  local name="$1"; shift
  selected "$name" || return 0
  echo ">> $name"
  local s=$SECONDS
  "$@"
  local rc=$?
  echo ">> $name exit $rc ($(( SECONDS - s ))s, $(( SECONDS - T0 ))s elapsed)"
  [[ $rc -eq 0 ]] || failed+=("$name")
}

# gofmt: fail listing every tracked Go file gofmt would change.
gofmtcheck() {
  local out
  out="$(git -C "$ROOT" ls-files '*.go' | sed "s|^|$ROOT/|" | xargs gofmt -l)" || return
  [[ -z "$out" ]] && return 0
  echo "$out"
  echo "   gofmt -w those files"
  return 1
}

tourwasm() {
  local log rc
  log="$(mktemp "${TMPDIR:-/tmp}/nomi-tourwasm.XXXXXX")"
  "$ROOT/scripts/build-tour-wasm.sh" >"$log" 2>&1
  rc=$?
  if [[ $rc -eq 0 ]]; then
    rm -f "$log"
  else
    tail -n 40 "$log"
    echo "   full log: $log"
  fi
  return $rc
}

buildvet() {
  go -C "$ROOT" build ./... && go -C "$ROOT" vet ./...
}

pkgs() {
  local list
  list="$(go -C "$ROOT" list ./... | grep -v -e '/internal/irbuild$' -e '/vmhost$' -e '/rt$' -e '/rt/')" || return
  # shellcheck disable=SC2086 # one package per word
  go -C "$ROOT" test $list -skip '^TestLibModules$' -count=1 -timeout 0
}

treesitter() {
  if ! command -v npx >/dev/null 2>&1; then
    echo "   npx is not on PATH; skipping the tree-sitter tests"
    return 0
  fi
  "$ROOT/scripts/tree-sitter.sh" test
}

# pstep NAME CMD...: start CMD in the background when NAME is selected,
# with its output in a log that pwait prints.
pids=()
pnames=()
plogs=()
pstep() {
  local name="$1"; shift
  selected "$name" || return 0
  local log
  log="$(mktemp "${TMPDIR:-/tmp}/nomi-check-$name.XXXXXX")"
  echo ">> $name started"
  (
    s=$SECONDS
    "$@"
    rc=$?
    echo ">> took $(( SECONDS - s ))s"
    exit $rc
  ) >"$log" 2>&1 &
  pids+=("$!")
  pnames+=("$name")
  plogs+=("$log")
}

# pwait: wait for every step pstep started, and print each one's output and
# exit status in the order they started.
pwait() {
  local i rc
  for i in "${!pids[@]}"; do
    wait "${pids[$i]}"
    rc=$?
    echo ">> ${pnames[$i]}"
    cat "${plogs[$i]}"
    rm -f "${plogs[$i]}"
    echo ">> ${pnames[$i]} exit $rc ($(( SECONDS - T0 ))s elapsed)"
    [[ $rc -eq 0 ]] || failed+=("${pnames[$i]}")
  done
  pids=()
  pnames=()
  plogs=()
}

fuzz() { # package target
  go -C "$ROOT" test "$1" -run '^$' -fuzz "^$2\$" -fuzztime "$FUZZTIME" -fuzzminimizetime 5s -parallel 2
}

# racecorpus: a race-built nomi runs the corpus directories whose programs
# start tasks, so a race in rt's or the VM's task, channel and supervisor
# code under a real program is reported (the Go race detector exits 66).
racecorpus() {
  local bin rc=0 d
  bin="$(mktemp -d "${TMPDIR:-/tmp}/nomi-race.XXXXXX")/nomi"
  go -C "$ROOT" build -race -o "$bin" ./cmd/nomi || return
  for d in 16-concurrency 15-app-and-defer; do
    (cd "$ROOT" && GORACE=halt_on_error=1 "$bin" test "tests/$d") || rc=1
  done
  rm -rf "$(dirname "$bin")"
  return $rc
}

step gofmt gofmtcheck
step buildvet buildvet
step tourwasm tourwasm

pstep pkgs pkgs
pstep vmhost env NOMI_TOUR_RUN_BUDGET=1 go -C "$ROOT" test ./vmhost -count=1 -timeout 0
pstep rt go -C "$ROOT" test ./rt/... -count=1 -timeout 0
pstep irbuild go -C "$ROOT" test ./internal/irbuild -count=1 -timeout 0
pstep lib go -C "$ROOT" test ./cmd/nomi -run '^TestLibModules$' -count=1 -timeout 0
pstep treesitter treesitter
pwait

# The race detector finds a race only where goroutines run, so it runs the
# tests that start them: rt whole, the language server's background work,
# and internal/vm's and vmhost's task, channel, cancellation and synctest
# tests, beside the corpus programs that run tasks (racecorpus).
pstep race-rt go -C "$ROOT" test -race ./rt/... -count=1 -timeout 0
pstep race-lsp go -C "$ROOT" test -race ./internal/lsp -run '^(TestIncremental_BodyEditsConcurrentWithRequests|TestLoweringFromAnalysis_ConcurrentWithRequests|TestLoweringFromAnalysis_LeavesTheAnalysisUnchanged|TestRPC_ImplImportCheckWhileSiblingIsRead|TestPropagateAsync_.*)$' -count=1 -timeout 0
pstep race-vm go -C "$ROOT" test -race ./internal/vm -run 'Task|Concurrent|Once|Writer|Debug|Travel|Cancel|Supervisor|Channel' -count=1 -timeout 0
pstep race-vmhost go -C "$ROOT" test -race ./vmhost -run 'Synctest|StdinCancel|SafePoint|Tasks|Concurrent|TestTimeout|Evaluate_Stops|ReaderSources|Capture_' -count=1 -timeout 0
pstep race-corpus racecorpus
pwait

for t in $FUZZ_VMHOST; do
  pstep "fuzz-$t" fuzz ./vmhost "$t"
done
pstep fuzz-FuzzIncrementalMatchesFull fuzz ./internal/analysis FuzzIncrementalMatchesFull
pstep fuzz-FuzzLSPSurvivesEdits fuzz ./internal/lsp FuzzLSPSurvivesEdits
pwait

if [[ ${#failed[@]} -gt 0 ]]; then
  echo ">> FAILED after $(( SECONDS - T0 ))s: ${failed[*]}"
  exit 1
fi
echo ">> every step passed in $(( SECONDS - T0 ))s"
