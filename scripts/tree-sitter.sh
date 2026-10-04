#!/usr/bin/env bash
# Run the tree-sitter CLI from the worktree's tree-sitter-nomi/ directory.
#
# The tree-sitter CLI operates on its current working directory (it has no
# "-C <dir>" flag), so this wrapper runs it from tree-sitter-nomi/ wherever it
# is invoked from. Running it inside tree-sitter-nomi/ also avoids the CLI's
# global parser-directories config, which may point at another checkout.
#
# Usage:
#   scripts/tree-sitter.sh generate
#   scripts/tree-sitter.sh test
#   scripts/tree-sitter.sh parse <file>
#   scripts/tree-sitter.sh <any tree-sitter subcommand + args>
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
grammar_dir="$here/../tree-sitter-nomi"

cd "$grammar_dir"
exec npx tree-sitter "$@"
