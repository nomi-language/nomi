#!/usr/bin/env bash
# test-cmd-nomi.sh: cmd/nomi's tests in parts, each part one test binary.
# The package is too long for one binary: most of its tests set the
# environment and so run one at a time. scripts/check-full.sh runs the parts
# at the same time, and .github/workflows/test.yml runs each part as a job of
# its own, on Linux and on Windows. Both take the parts from `parts` here.
#
# The parts, by test name:
#   1    Test[A-C]... and the fuzz targets' seeds
#   2    Test[D-R]...
#   3    every other test: the complement of the parts above and lib
#   lib  TestLibModules, the Nomi tests of the modules under lib/
# Part 3 is what the other parts' patterns leave, so a test or an example
# with any name runs in exactly one part.
#
# Usage:
#   scripts/test-cmd-nomi.sh parts            print the part names, a line each
#   scripts/test-cmd-nomi.sh PART [FLAG...]   run PART; each FLAG goes to go test
#                                             (-timeout 30m, -v)

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# NAMED holds each part chosen by a -run pattern, in order; the last part
# runs what none of them, nor lib, matches.
NAMED=('^(Test[A-C]|Fuzz)' '^Test[D-R]')
LIB='^TestLibModules$'
LAST=$(( ${#NAMED[@]} + 1 ))

if [[ $# -lt 1 ]]; then
  echo "usage: $0 parts | PART [go test flag...]" >&2
  exit 2
fi
part="$1"; shift

if [[ $part == parts ]]; then
  for (( i = 1; i <= LAST; i++ )); do
    echo "$i"
  done
  echo lib
  exit 0
fi

if [[ $part == lib ]]; then
  sel=(-run "$LIB")
elif [[ $part =~ ^[0-9]+$ && $part -ge 1 && $part -lt $LAST ]]; then
  sel=(-run "${NAMED[$(( part - 1 ))]}" -skip "$LIB")
elif [[ $part == "$LAST" ]]; then
  skip="$LIB"
  for p in "${NAMED[@]}"; do
    skip="$skip|$p"
  done
  sel=(-skip "$skip")
else
  echo "$0: no part named $part; the parts are: $("$0" parts | tr '\n' ' ')" >&2
  exit 2
fi

exec go -C "$ROOT" test ./cmd/nomi "${sel[@]}" -count=1 "$@"
