#!/usr/bin/env bash
# test-go-packages.sh: the Go packages CI tests, in groups, each group a job
# of its own on Linux and on Windows (.github/workflows/test.yml, which takes
# the groups from `groups` here). In one job the packages shared a runner's
# four cores: internal/expectation, 150 s alone, took 1368 s there and set
# the job's 29 minutes.
#
# The groups:
#   expectation  internal/expectation
#   analysis     internal/analysis
#   lsp          internal/lsp
#   vmhost       vmhost and internal/vm
#   rest         every other package: what `go list ./...` names, less the
#                groups above, internal/irbuild and cmd/nomi, which have jobs
#                of their own
# rest is what the others leave, so a package with any path is tested in
# exactly one job.
#
# Usage:
#   scripts/test-go-packages.sh groups             print the group names, a line each
#   scripts/test-go-packages.sh GROUP [FLAG...]    test GROUP; each FLAG goes to go test
#                                                  (-timeout 40m, -p 4)
#   scripts/test-go-packages.sh list GROUP         print GROUP's packages, a line each

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE=github.com/nomi-language/nomi

# NAMES and PACKAGES hold each named group and its packages, by position.
NAMES=(expectation analysis lsp vmhost)
PACKAGES=(
  "./internal/expectation"
  "./internal/analysis"
  "./internal/lsp"
  "./vmhost ./internal/vm"
)
# ELSEWHERE are the packages with jobs of their own.
ELSEWHERE="./internal/irbuild ./cmd/nomi"

if [[ $# -lt 1 ]]; then
  echo "usage: $0 groups | GROUP [go test flag...] | list GROUP" >&2
  exit 2
fi
list=0
if [[ $1 == list && $# -ge 2 ]]; then
  list=1; shift
fi
group="$1"; shift

if [[ $group == groups ]]; then
  printf '%s\n' "${NAMES[@]}" rest
  exit 0
fi

pkgs=()
if [[ $group == rest ]]; then
  taken=" $ELSEWHERE ${PACKAGES[*]} "
  while IFS= read -r pkg; do
    rel="./${pkg#"$MODULE"/}"
    [[ $pkg == "$MODULE" ]] && rel="."
    [[ $taken == *" $rel "* ]] || pkgs+=("$rel")
  done < <(go -C "$ROOT" list ./...)
else
  for (( i = 0; i < ${#NAMES[@]}; i++ )); do
    if [[ ${NAMES[$i]} == "$group" ]]; then
      read -r -a pkgs <<< "${PACKAGES[$i]}"
    fi
  done
fi
if [[ ${#pkgs[@]} -eq 0 ]]; then
  echo "$0: no group named $group; the groups are: $("$0" groups | tr '\n' ' ')" >&2
  exit 2
fi

if [[ $list == 1 ]]; then
  printf '%s\n' "${pkgs[@]}"
  exit 0
fi
exec go -C "$ROOT" test -count=1 "$@" "${pkgs[@]}"
