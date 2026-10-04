// Finds `//!` attached tests and the parts of them to draw dimmed. Neovim,
// Helix and Zed find them with tree-sitter's attached_test_prompt node
// (editors/nvim/lua/nomi/attached_tests.lua); VS Code has no tree-sitter, so
// this scans the text the way tree-sitter-nomi's scanner groups prompts: a
// line whose first non-blank characters are `//!` is a prompt, and
// consecutive prompt lines form one attached test. Any other line, blank or
// not, ends it.

const prompt = /^(\s*)\/\/!/;

/** An attached test's lines, 0-based and inclusive. */
export interface Group {
  first: number;
  last: number;
}

/** A dimmed span on one line, in 0-based UTF-16 columns, end exclusive. */
export interface Span {
  line: number;
  start: number;
  end: number;
}

/** Every attached test in `lines`, in order. */
export function attachedTestGroups(lines: readonly string[]): Group[] {
  const groups: Group[] = [];
  let open: Group | undefined;
  lines.forEach((line, i) => {
    if (!prompt.test(line)) {
      open = undefined;
    } else if (open) {
      open.last = i;
    } else {
      open = { first: i, last: i };
      groups.push(open);
    }
  });
  return groups;
}

/**
 * The groups any of `selections` touches: a selection is a [first, last]
 * line pair, 0-based and inclusive.
 */
export function groupsUnder(groups: readonly Group[], selections: readonly [number, number][]): Group[] {
  return groups.filter((g) => selections.some(([first, last]) => first <= g.last && last >= g.first));
}

/**
 * The spans to dim: on each line of each group not in `revealed`, everything
 * after the `//!` marker. The marker keeps its comment color.
 */
export function dimSpans(lines: readonly string[], groups: readonly Group[], revealed: readonly Group[] = []): Span[] {
  const spans: Span[] = [];
  for (const g of groups) {
    if (revealed.some((r) => r.first === g.first)) {
      continue;
    }
    for (let line = g.first; line <= g.last; line++) {
      const m = prompt.exec(lines[line]);
      const start = m ? m[0].length : 0;
      const end = lines[line].length;
      if (start < end) {
        spans.push({ line, start, end });
      }
    }
  }
  return spans;
}

/** Splits text into lines without their line ends. */
export function splitLines(text: string): string[] {
  return text.split(/\r?\n/);
}
