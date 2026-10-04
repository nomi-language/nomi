#!/usr/bin/env bash
# Generate editors/helix/runtime/queries/nomi/ and set up Helix for Nomi.
#
#   scripts/sync-helix-runtime.sh            regenerate the query mirrors only
#   scripts/sync-helix-runtime.sh --install  also copy the queries into your
#                                            Helix config (a snapshot), write
#                                            the languages.toml block and build
#                                            the grammar
#   scripts/sync-helix-runtime.sh --dev      the same, but link the queries
#                                            directory to this checkout, so
#                                            query changes need no reinstall
#
# Both setup modes build the grammar from this checkout's tree-sitter-nomi/.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HELIX_NOMI="$ROOT/editors/helix"
QUERY_DIR="$HELIX_NOMI/runtime/queries/nomi"
CONFIG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}"
USER_HELIX="$CONFIG_HOME/helix"
LANGUAGES="$HELIX_NOMI/languages.toml"

mkdir -p "$QUERY_DIR"
cp "$ROOT/tree-sitter-nomi/queries/injections.scm" "$QUERY_DIR/injections.scm"
python3 - "$ROOT" "$QUERY_DIR/highlights.scm" <<'PY'
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
target = pathlib.Path(sys.argv[2])

# Helix applies overlapping captures in last-wins order, like Zed. Keep the
# Helix copy ordered from catch-alls to specifics, but translate Zed/source
# scopes to names that Helix's bundled themes style consistently.
text = (root / "editors/zed/languages/nomi/highlights.scm").read_text()
text = text.replace("@property", "@variable.other.member")
text = text.replace("@number", "@constant.numeric")
text = text.replace("@module", "@namespace")
text = text.replace("(codepoint) @string.special", "(codepoint) @constant.character")
text = text.replace(
    '(string_interpolation\n  "#{" @punctuation.special\n  "}" @punctuation.special)',
    '(string_interpolation\n  "#{" @constant.character.escape\n  "}" @constant.character.escape)',
)
text = text.replace("Zed's query priority", "Helix's query priority")
text = text.replace("Zed's last-wins priority", "Helix's last-wins priority")
text = text.replace("Zed layers semantic tokens", "editors layer semantic tokens")
target.write_text(text)
PY

mode="${1:-}"
case "$mode" in
  "") echo "Synced Helix Nomi runtime in $HELIX_NOMI"; exit 0 ;;
  --install | --dev) ;;
  *) echo "usage: scripts/sync-helix-runtime.sh [--install | --dev]" >&2; exit 2 ;;
esac

mkdir -p "$USER_HELIX/runtime/queries" "$USER_HELIX/runtime/grammars"
USER_QUERIES="$USER_HELIX/runtime/queries/nomi"
if [[ "$mode" == "--dev" ]]; then
  rm -rf "$USER_QUERIES"
  ln -s "$QUERY_DIR" "$USER_QUERIES"
else
  if [[ -L "$USER_QUERIES" ]]; then
    rm "$USER_QUERIES"
  fi
  mkdir -p "$USER_QUERIES"
  cp "$QUERY_DIR/"*.scm "$USER_QUERIES/"
fi

  mkdir -p "$USER_HELIX"
  touch "$USER_HELIX/languages.toml"

  python3 - "$ROOT" "$LANGUAGES" "$USER_HELIX/languages.toml" <<'PY'
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
source_path = pathlib.Path(sys.argv[2])
target_path = pathlib.Path(sys.argv[3])
grammar = root / "tree-sitter-nomi"

source_lines = []
for line in source_path.read_text().splitlines():
    if line.startswith("source = { "):
        line = f'source = {{ path = "{grammar}" }}'
    source_lines.append(line)
source = "\n".join(source_lines).strip()

target = target_path.read_text()
begin = "# BEGIN NOMI HELIX SUPPORT"
end = "# END NOMI HELIX SUPPORT"
block = f"{begin}\n{source}\n{end}"

if begin in target and end in target:
    before, rest = target.split(begin, 1)
    _, after = rest.split(end, 1)
    next_text = before.rstrip() + "\n\n" + block + after
else:
    next_text = target.rstrip()
    if next_text:
        next_text += "\n\n"
    next_text += block + "\n"

target_path.write_text(next_text)
PY

# Build only Nomi's grammar: `hx --grammar build` against the real config would
# build every grammar Helix knows, and one unrelated failure fails the run. The
# temporary config is the user's languages.toml with `use-grammars` replaced;
# its runtime/ links to the real one, so nomi.so lands in the user's config.
tmp="$(mktemp -d "${TMPDIR:-/tmp}/nomi-helix.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/helix/runtime"
ln -s "$USER_HELIX/runtime/grammars" "$tmp/helix/runtime/grammars"
ln -s "$USER_HELIX/runtime/queries" "$tmp/helix/runtime/queries"
{
  printf 'use-grammars = { only = ["nomi"] }\n\n'
  grep -v '^[[:space:]]*use-grammars[[:space:]]*=' "$USER_HELIX/languages.toml" || true
} > "$tmp/helix/languages.toml"
XDG_CONFIG_HOME="$tmp" hx --grammar build

if [[ "$mode" == "--dev" ]]; then
  echo "Linked $USER_QUERIES to $QUERY_DIR and built the grammar"
else
  echo "Installed Nomi Helix support into $USER_HELIX"
fi
