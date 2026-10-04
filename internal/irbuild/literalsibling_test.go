package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiteralSibling_TwoProvidersOfOneHandlerAreRejectedByTheFrontEnd holds
// that a typed literal whose type has two providers of `from_fragments` is a
// front-end error (`checkTaggedString`'s ambiguity diagnostic;
// `analysis.TestTypedLiteral_AmbiguousHandlerIsRejected` pins the wording).
// That makes `lowerTaggedLiteral`'s `rivalled` arm unreachable through
// `Analyze`, a fail-safe rather than a live route. Without the diagnostic, the
// builder would have to pick between the inherent and the interface provider,
// and the front end's halves disagree on which one wins depending on
// declaration order.
//
// The rows are spelling by layout. There are two spellings of a second
// `from_fragments` on a receiver: an inherent `impl Pick` block, and a second
// INTERFACE that declares the name (`defineImplBlockAnnotations` admits both).
// There are two layouts because the entry-file and sibling-file cases are
// different resolutions in the front end (per-file table vs project index),
// and one of them passing proves nothing about the other.
func TestLiteralSibling_TwoProvidersOfOneHandlerAreRejectedByTheFrontEnd(t *testing.T) {
	const handler = "impl Literal for Pick {\n" +
		"  fn from_fragments(fragments: List<Fragment<String>>): String { _ = fragments;\n    \"iface\"\n  }\n}\n\n"
	const inherentRival = "impl Pick {\n" +
		"  pub fn from_fragments(_fragments: List<Fragment<String>>): String {\n    \"inherent\"\n  }\n}\n"
	const ifaceRival = "interface Other {\n  fn from_fragments(fragments: List<Fragment<String>>): Int\n}\n\n" +
		"impl Other for Pick {\n" +
		"  fn from_fragments(fragments: List<Fragment<String>>): Int { _ = fragments;\n    7\n  }\n}\n"
	for _, spelling := range []struct {
		name  string
		decls string
	}{
		{"inherent rival", "pub type Pick\n\n" + handler + inherentRival},
		{"second interface rival", "pub type Pick\n\n" + handler + ifaceRival},
	} {
		for _, tc := range []struct {
			name    string
			sibling string
			entry   string
		}{{
			name:  "declared in the entry file",
			entry: "import std/literals.{Fragment, Literal}\n\n" + spelling.decls + "\nfn main() {\n  _ = Pick`x`\n}\n",
		}, {
			name:    "declared in a sibling file",
			sibling: "import std/literals.{Fragment, Literal}\n\n" + spelling.decls,
			entry:   "import pick.Pick\n\nfn main() {\n  _ = Pick`x`\n}\n",
		}} {
			t.Run(spelling.name+", "+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				if tc.sibling != "" {
					writeNomi(t, dir, "pick.nomi", tc.sibling)
				}
				writeNomi(t, dir, "main.nomi", tc.entry)
				_, err := Analyze(filepath.Join(dir, "main.nomi"))
				if err == nil {
					t.Fatal("the front end accepted a typed literal with two providers of one " +
						"`from_fragments`, so the builder is the only component refusing it " +
						"and `lowerTaggedLiteral`'s `rivalled` arm is live rather than a fail-safe")
				}
				if !strings.Contains(err.Error(), "is ambiguous") {
					t.Fatalf("rejected for the wrong reason; want the typed-literal ambiguity, got: %v", err)
				}
			})
		}
	}
}

// writeNomi writes one file of a multi-file fixture built in a temp dir.
func writeNomi(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
