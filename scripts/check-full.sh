#!/usr/bin/env bash
# check-full.sh: the full check a batch passes before `make publish-main`
# squashes next to main (AGENTS.md, "Verification cadence").
#
# Steps, in order: gofmt over the tracked Go files, the tour wasm bundle,
# build and vet, every package but internal/irbuild, vmhost and rt
# (TestLibModules apart), vmhost alone at the default -parallel, rt, internal/irbuild alone, TestLibModules, the
# tree-sitter tests (when npx is on PATH), the race detector over the
# concurrent packages, then each fuzz target for FUZZTIME. Every step runs
# even when an earlier one fails; each prints its exit status and the time
# elapsed, and the script exits 1 when any step failed.
#
# Usage:
#   scripts/check-full.sh
#
#   FUZZTIME=5m       how long each fuzz target runs (default 5m)
#   STEPS="a b ..."   run only the named steps: gofmt tourwasm buildvet pkgs
#                     vmhost rt irbuild lib treesitter race, and fuzz-<Target>
#                     (fuzz-FuzzFormatKeepsMeaning). Empty runs every step.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FUZZTIME="${FUZZTIME:-5m}"
STEPS="${STEPS:-}"
T0=$SECONDS
failed=()

FUZZ_VMHOST="FuzzFrontEndAcceptsSoItLowers FuzzFormatKeepsMeaning FuzzLoweredProgramsRun"
KNOWN="gofmt tourwasm buildvet pkgs vmhost rt irbuild lib treesitter race"
for t in $FUZZ_VMHOST FuzzIncrementalMatchesFull FuzzLSPSurvivesEdits; do
  KNOWN="$KNOWN fuzz-$t"
done
for name in $STEPS; do
  if [[ " $KNOWN " != *" $name "* ]]; then
    echo "check-full: no step named $name; the steps are: $KNOWN" >&2
    exit 2
  fi
done

# step NAME CMD...: run CMD when STEPS is empty or names NAME.
step() {
  local name="$1"; shift
  if [[ -n "$STEPS" && " $STEPS " != *" $name "* ]]; then
    return 0
  fi
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

fuzz() { # package target
  go -C "$ROOT" test "$1" -run '^$' -fuzz "^$2\$" -fuzztime "$FUZZTIME" -fuzzminimizetime 5s -parallel 4
}

step gofmt gofmtcheck
step tourwasm tourwasm
step buildvet buildvet
step pkgs pkgs
step vmhost env NOMI_TOUR_RUN_BUDGET=1 go -C "$ROOT" test ./vmhost -count=1 -timeout 0
step rt go -C "$ROOT" test ./rt/... -count=1 -timeout 0
step irbuild go -C "$ROOT" test ./internal/irbuild -count=1 -timeout 0
step lib go -C "$ROOT" test ./cmd/nomi -run '^TestLibModules$' -count=1 -timeout 0
step treesitter treesitter
step race go -C "$ROOT" test -race ./internal/vm ./vmhost ./rt/... ./internal/lsp -count=1 -timeout 0
for t in $FUZZ_VMHOST; do
  step "fuzz-$t" fuzz ./vmhost "$t"
done
step fuzz-FuzzIncrementalMatchesFull fuzz ./internal/analysis FuzzIncrementalMatchesFull
step fuzz-FuzzLSPSurvivesEdits fuzz ./internal/lsp FuzzLSPSurvivesEdits

if [[ ${#failed[@]} -gt 0 ]]; then
  echo ">> FAILED after $(( SECONDS - T0 ))s: ${failed[*]}"
  exit 1
fi
echo ">> every step passed in $(( SECONDS - T0 ))s"
