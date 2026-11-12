#!/usr/bin/env bash
# check-full.sh: the full check a batch passes before `make publish-main`
# squashes next to main (AGENTS.md, "Verification cadence").
#
# Steps, in four phases. First, in order: gofmt over the tracked Go files,
# build and vet, the tour wasm bundle (vmhost's TestTourWasm* read it).
# Then, at the same time:
#   - pkgs: every other package, without the expectation suites and the
#     object harnesses, which have steps of their own;
#   - cmd/nomi in two parts (cmdnomi-1, cmdnomi-2), vmhost, rt and
#     internal/irbuild, each alone; lib (TestLibModules); the tree-sitter
#     tests (when npx is on PATH);
#   - the object harness over the expectation populations in full: every
#     recorded program linked through objects (objects-link), and with each
#     of its files lowered again on its own against the others' kept objects
#     (objects-perfile); and every program irbuild.expect records, run
#     linked through objects (objects-irbuild);
#   - the expectation suites, corpus, stdlib, tour and failure, each a step
#     (interfaces-<suite>), with every project file checked again against
#     interfaces; each compares its golden file as the plain run does.
# Then the race detector, its parts at the same time: rt and the language
# server's concurrent tests whole or filtered, internal/vm's and vmhost's
# task, channel, cancellation and synctest tests, and a race-built `nomi
# test` over the corpus directories whose programs run tasks. The fuzz
# targets' saved inputs replay in pkgs and vmhost as ordinary tests. The
# fuzz targets themselves run only when asked (FUZZ=1, or STEPS naming them;
# `make fuzz`), every one at the same time for FUZZTIME, two workers each:
# they run after a push, not before it, and what they find is fixed on next
# for the following batch.
# Every step runs even when an earlier one fails; each prints its output,
# its exit status and its time, and the script exits 1 when any step
# failed.
#
# Usage:
#   scripts/check-full.sh
#
#   FUZZ=1            also run the fuzz targets (`make fuzz` runs only them)
#   FUZZTIME=5m       how long each fuzz target runs (default 5m)
#   STEPS="a b ..."   run only the named steps: gofmt tourwasm buildvet pkgs
#                     vmhost rt irbuild lib treesitter, cmdnomi-<part>
#                     (cmdnomi-1 cmdnomi-2), interfaces-<suite>
#                     (interfaces-corpus interfaces-stdlib interfaces-tour
#                     interfaces-failure), objects-<part> (objects-link
#                     objects-perfile objects-irbuild), race-<part> (race-rt
#                     race-lsp race-vm race-vmhost race-corpus) and
#                     fuzz-<Target> (fuzz-FuzzFormatKeepsMeaning);
#                     `cmdnomi`, `interfaces`, `objects`, `race` and `fuzz`
#                     name every part. Empty runs every step but the fuzz
#                     targets, which FUZZ=1 adds.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FUZZTIME="${FUZZTIME:-5m}"
STEPS="${STEPS:-}"
FUZZ="${FUZZ:-0}"
T0=$SECONDS
failed=()

FUZZ_VMHOST="FuzzFrontEndAcceptsSoItLowers FuzzFormatKeepsMeaning FuzzLoweredProgramsRun"
KNOWN="gofmt tourwasm buildvet pkgs cmdnomi cmdnomi-1 cmdnomi-2 vmhost rt irbuild lib treesitter interfaces interfaces-corpus interfaces-stdlib interfaces-tour interfaces-failure objects objects-link objects-perfile objects-irbuild race race-rt race-lsp race-vm race-vmhost race-corpus fuzz"
for t in $FUZZ_VMHOST FuzzIncrementalMatchesFull FuzzCheckFromInterfacesMatchesFull FuzzLSPSurvivesEdits; do
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
  if [[ -z "$STEPS" ]]; then
    [[ "$1" != fuzz-* || "$FUZZ" == 1 ]]
    return
  fi
  [[ " $STEPS " == *" $1 "* || " $STEPS " == *" ${1%%-*} "* ]]
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

# pkgs: every package but internal/irbuild, vmhost, rt and cmd/nomi, which
# run as steps of their own. It skips the tests other steps run in full:
# the four expectation suites are the interfaces-* steps, which compare
# every record as the plain run does and check each program against
# interfaces too, and the object harnesses, which here would run a sample,
# are objects-link and objects-perfile.
pkgs() {
  local list
  list="$(go -C "$ROOT" list ./... | grep -v -e '/internal/irbuild$' -e '/vmhost$' -e '/rt$' -e '/rt/' -e '/cmd/nomi$')" || return
  # shellcheck disable=SC2086 # one package per word
  go -C "$ROOT" test $list -skip '^(TestExpectation_(Corpus|Stdlib|Tour|Failure)|TestObjectsLinkToTheSameProgram|TestFilesLowerOnTheirOwnAsInTheProgram)$' -count=1 -timeout 0
}

# cmdnomi PART: cmd/nomi's tests but TestLibModules (the lib step), in two
# processes, since most of them set the environment and so run one at a
# time: part 1 the tests named Test[A-C]... and the fuzz targets' seeds,
# part 2 the rest, each about half the package's time.
cmdnomi() {
  local part='^(Test[A-C]|Fuzz)'
  if [[ $1 == 1 ]]; then
    go -C "$ROOT" test ./cmd/nomi -run "$part" -count=1 -timeout 0
  else
    go -C "$ROOT" test ./cmd/nomi -skip "$part|^TestLibModules\$" -count=1 -timeout 0
  fi
}

# interfaces SUITE: one expectation suite with every project file checked
# again against interfaces (NOMI_CHECK_FROM_INTERFACES).
interfaces() {
  NOMI_CHECK_FROM_INTERFACES=1 go -C "$ROOT" test ./internal/expectation -run "^TestExpectation_$1\$" -count=1 -timeout 0
}

treesitter() {
  if ! command -v npx >/dev/null 2>&1; then
    echo "   npx is not on PATH; skipping the tree-sitter tests"
    return 0
  fi
  "$ROOT/scripts/tree-sitter.sh" test
}

# cpuseconds FILE: the user and system CPU seconds of a shell's finished
# children, from the second line of the `times` output in FILE. `times`
# must run in that shell itself, since in a pipe or a command substitution
# it reports a fresh subshell's.
cpuseconds() {
  awk 'function sec(t,  m) { m = t; sub(/m.*/, "", m); sub(/^[0-9]+m/, "", t); sub(/s$/, "", t); return m * 60 + t }
    NR == 2 { printf "%d", sec($1) + sec($2) }' "$1"
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
    # The CPU the step's processes used, user and system, which matters
    # because with every step at once the machine's cores are the limit.
    times >"$log.times"
    echo ">> took $(( SECONDS - s ))s, cpu $(cpuseconds "$log.times")s"
    rm -f "$log.times"
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
pstep cmdnomi-1 cmdnomi 1
pstep cmdnomi-2 cmdnomi 2
pstep vmhost env NOMI_TOUR_RUN_BUDGET=1 go -C "$ROOT" test ./vmhost -count=1 -timeout 0
pstep rt go -C "$ROOT" test ./rt/... -count=1 -timeout 0
pstep irbuild go -C "$ROOT" test ./internal/irbuild -count=1 -timeout 0
pstep lib go -C "$ROOT" test ./cmd/nomi -run '^TestLibModules$' -count=1 -timeout 0
pstep treesitter treesitter
pstep objects-link env NOMI_OBJECTS_LINK=full go -C "$ROOT" test ./internal/expectation -run '^TestObjectsLinkToTheSameProgram$' -count=1 -timeout 0
pstep objects-perfile env NOMI_OBJECTS_LINK=full go -C "$ROOT" test ./internal/expectation -run '^TestFilesLowerOnTheirOwnAsInTheProgram$' -count=1 -timeout 0
pstep objects-irbuild env NOMI_LINK_OBJECTS=1 go -C "$ROOT" test ./internal/irbuild -run '^TestIRGolden_RecordsMatchTheVM$' -count=1 -timeout 0
for s in Corpus Stdlib Tour Failure; do
  pstep "interfaces-$(tr '[:upper:]' '[:lower:]' <<<"$s")" interfaces "$s"
done
pwait

# The race detector finds a race only where goroutines run, so it runs the
# tests that start them: rt whole, the language server's background work,
# and internal/vm's and vmhost's task, channel, cancellation and synctest
# tests, beside the corpus programs that run tasks (racecorpus). They run
# in a phase of their own: several carry wall-clock limits (a `nomi test`
# case's 10 s, the debugger's pause), which a race-built binary misses on a
# machine the steps above saturate.
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
pstep fuzz-FuzzCheckFromInterfacesMatchesFull fuzz ./internal/analysis FuzzCheckFromInterfacesMatchesFull
pstep fuzz-FuzzLSPSurvivesEdits fuzz ./internal/lsp FuzzLSPSurvivesEdits
pwait

if [[ ${#failed[@]} -gt 0 ]]; then
  echo ">> FAILED after $(( SECONDS - T0 ))s: ${failed[*]}"
  exit 1
fi
echo ">> every step passed in $(( SECONDS - T0 ))s"
