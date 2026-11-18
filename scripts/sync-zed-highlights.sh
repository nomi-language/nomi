#!/usr/bin/env bash
# Generate editors/zed/languages/nomi/highlights.scm from the canonical
# query, tree-sitter-nomi/queries/highlights.scm.
#
#   scripts/sync-zed-highlights.sh          write the file
#   scripts/sync-zed-highlights.sh --check  fail if the file is not current
#
# tree-sitter's highlighter gives a node the first pattern that captures it,
# so the canonical query lists specific patterns first. Zed gives it the
# last, so Zed's query is the canonical one with its patterns in reverse
# order, the capture names Zed's themes style (the renames below), and
# Zed's attached-test dim (editors/zed/attached-test-dim.scm, written by
# hand) just under the `//!` markers, the one pattern that outranks it.
#
# Helix's and Neovim's queries are generated from Zed's
# (scripts/sync-helix-runtime.sh, scripts/sync-nvim-runtime.sh), so this
# runs first: `make build-zed-queries`, which `make build-helix` and
# `make build-nvim` run. TestZedHighlightsAreGenerated (internal/docscheck)
# runs --check.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

python3 - "$ROOT" "${1:-}" <<'PY'
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
mode = sys.argv[2]
SOURCE = "tree-sitter-nomi/queries/highlights.scm"
FRAGMENT = "editors/zed/attached-test-dim.scm"
TARGET = "editors/zed/languages/nomi/highlights.scm"

# Capture names the canonical query uses that Zed's themes do not style,
# and the name Zed gets instead.
RENAMES = {
    "@variable.other.member": "@property",
    "@function.definition": "@function",
    "@function.call": "@function",
    "@type.definition": "@type",
    "@character": "@string.special",
}

# The section headings that state the order, and what they say reversed.
HEADINGS = {
    "; === Specific patterns (highest priority — listed first for tree-sitter) ===":
        "; === Specific patterns (highest priority — listed last here) ===",
    "; === Catch-all patterns (lowest priority — listed last for tree-sitter) ===":
        "; === Catch-all patterns (lowest priority — listed first here) ===",
}


def fail(msg):
    sys.exit(f"sync-zed-highlights: {msg}")


def units(text):
    """The query's top-level units in order: ("blank",), ("comment", text)
    for a run of comment lines, ("pattern", text) for one pattern."""
    out = []
    lines = text.rstrip("\n").split("\n")
    i = 0
    while i < len(lines):
        line = lines[i]
        if not line.strip():
            out.append(("blank", ""))
            i += 1
        elif line.lstrip().startswith(";"):
            j = i
            while j < len(lines) and lines[j].lstrip().startswith(";"):
                j += 1
            out.append(("comment", "\n".join(lines[i:j])))
            i = j
        else:
            # A pattern runs to the line that closes its brackets.
            depth, j = 0, i
            while True:
                if j == len(lines):
                    fail(f"{SOURCE}: the pattern at line {i + 1} never closes")
                in_string, k, text_line = False, 0, lines[j]
                while k < len(text_line):
                    ch = text_line[k]
                    if in_string:
                        if ch == "\\":
                            k += 1
                        elif ch == '"':
                            in_string = False
                    elif ch == '"':
                        in_string = True
                    elif ch == ";":
                        break
                    elif ch in "([":
                        depth += 1
                    elif ch in ")]":
                        depth -= 1
                    k += 1
                j += 1
                if depth == 0:
                    break
            out.append(("pattern", "\n".join(lines[i:j])))
            i = j
    return out


def blocks(us):
    """Runs of units between blank lines, each a (lead, body) pair. A run
    of comments alone, other than a section heading, introduces the block
    after it: it is that block's lead, written above it with the blank
    line between them."""
    out, lead, cur = [], [], []
    for u in us + [("blank", "")]:
        if u[0] != "blank":
            cur.append(u)
            continue
        if not cur:
            continue
        if all(kind == "comment" for kind, _ in cur) and not is_heading(cur):
            lead += cur
        else:
            out.append((lead, cur))
            lead = []
        cur = []
    if lead:
        fail(f"{SOURCE}: a comment at the end of the file introduces nothing")
    return out


def is_heading(block):
    return len(block) == 1 and block[0][0] == "comment" and block[0][1].startswith("; ===")


def reverse_block(lead, body):
    """The block as text with its patterns in reverse order, each comment
    still above the patterns it was written above."""
    groups = []
    for kind, text in body:
        if kind == "comment" or not groups:
            groups.append([])
        groups[-1].append((kind, text))
    lines = [text for _, text in lead]
    if lead:
        lines.append("")
    for group in reversed(groups):
        lines += [text for kind, text in group if kind == "comment"]
        lines += [text for kind, text in reversed(group) if kind == "pattern"]
    return "\n".join(lines)


source = (root / SOURCE).read_text()
all_blocks = blocks(units(source))

# Sections: a `; === ... ===` heading and the blocks under it.
sections = []
for lead, body in all_blocks:
    if is_heading(body):
        sections.append((body[0][1], []))
    elif not sections:
        fail(f"{SOURCE}: patterns stand before the first `; === ... ===` heading")
    else:
        sections[-1][1].append((lead, body))

# The dim goes just under the canonical query's first pattern, the `//!`
# markers, which must go on winning over it.
first = sections[0][1][0]
if "attached_comment_test_prompt_marker" not in first[1][0][1]:
    fail(f"{SOURCE}: the first pattern is no longer the `//!` markers, which the attached-test dim goes under; update the generator")
fragment = (root / FRAGMENT).read_text().rstrip("\n")

parts = []
for heading, section in reversed(sections):
    if "tree-sitter" in heading:
        if heading not in HEADINGS:
            fail(f"{SOURCE}: no reversed wording for the heading {heading!r}; add it to HEADINGS")
        heading = HEADINGS[heading]
    parts.append(heading)
    for block in reversed(section):
        if block is first:
            parts.append(fragment)
        parts.append(reverse_block(*block))

text = "\n\n".join(parts) + "\n"
for old, new in RENAMES.items():
    if old not in text:
        fail(f"{SOURCE}: no {old} capture to rename to {new}; drop it from RENAMES")
    text = text.replace(old, new)

# The first paragraph names this generator; the Helix and Neovim generators
# put their own in its place and keep the second.
header = (
    f"; GENERATED by scripts/sync-zed-highlights.sh from {SOURCE}\n"
    f"; and {FRAGMENT}. Do not edit; change those and run\n"
    "; `make build-zed-queries`.\n"
    "\n"
    "; The last pattern that captures a node wins here, where tree-sitter's\n"
    "; highlighter gives a node the first, so the patterns are the canonical\n"
    "; query's (tree-sitter-nomi/queries/highlights.scm) in reverse order. A\n"
    "; comment is the canonical query's: where it says a pattern is listed\n"
    "; first, or above or below another, read the opposite here.\n\n"
)
text = header + text

target = root / TARGET
if mode == "--check":
    if not target.exists() or target.read_text() != text:
        fail(f"{TARGET} is not current; run `make build-zed-queries`")
elif mode == "":
    target.write_text(text)
    print(f"Wrote {TARGET}")
else:
    sys.exit("usage: scripts/sync-zed-highlights.sh [--check]")
PY
