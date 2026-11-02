package rt

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
)

// captureStdout runs f with os.Stdout replaced by a pipe and returns what it
// wrote.
//
// Dbg writes to os.Stdout by design — see its header for why the writer is not a
// parameter — so observing it needs the fd swapped. A pipe rather than a
// temporary file, because the colour decision reads
// `Mode()&os.ModeCharDevice` and a pipe is what a piped run gives the process:
// a regular file would exercise a writer no real run has.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	f()
	os.Stdout = saved
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// TestDbg_ThreeShapes pins rt.Dbg's layout directly, including the one shape no
// Nomi source can produce.
//
// The single-line and multi-line shapes are pinned end to end as well, by
// internal/irbuild's testdata/dbg_shapes.nomi against its golden record. The
// empty shape is not, and cannot be: `dbg`'s operand text is
// `format.RenderNode` of a real AST node, which is never empty, so that arm is
// reachable only from here. Dropping it would leave a stray space in the line
// if it were ever reached.
func TestDbg_ThreeShapes(t *testing.T) {
	for _, tc := range []struct{ name, expr, want string }{
		{
			name: "empty operand",
			expr: "",
			want: "dbg line 7: = 42\n",
		},
		{
			name: "single-line operand",
			expr: "41 + 1",
			want: "dbg line 7: 41 + 1 = 42\n",
		},
		{
			name: "multi-line operand",
			expr: "Rev{\n  zeta: 1,\n}",
			want: "dbg line 7:\n  Rev{\n    zeta: 1,\n  }\n  = 42\n",
		},
		{
			// A blank source line, such as the one between two case arms, is
			// printed empty rather than as the indent alone.
			name: "multi-line operand with a blank line",
			expr: "case n {\n    1 -> 42\n\n    _ -> 0\n}",
			want: "dbg line 7:\n  case n {\n      1 -> 42\n\n      _ -> 0\n  }\n  = 42\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := captureStdout(t, func() { Dbg(7, tc.expr, "42") })
			if got != tc.want {
				t.Fatalf("Dbg(7, %q, \"42\")\n want %q\n  got %q", tc.expr, tc.want, got)
			}
		})
	}
}

// TestDbg_TrimSpaceRunsBeforeTheNewlineTest pins the order of two statements,
// which decides the shape rather than merely tidying the text.
//
// DbgText runs `expr = strings.TrimSpace(expr)` before
// `strings.Contains(expr, "\n")`. Reversed, an operand whose text has a leading
// or trailing newline would take the multi-line branch and print an empty
// indented source line. Every input below is multi-line before trimming and
// single-line after, so each one fails if the order is swapped and none can pass
// by coincidence.
func TestDbg_TrimSpaceRunsBeforeTheNewlineTest(t *testing.T) {
	for _, expr := range []string{"\n41 + 1", "41 + 1\n", "  41 + 1  \n"} {
		got := captureStdout(t, func() { Dbg(7, expr, "42") })
		if want := "dbg line 7: 41 + 1 = 42\n"; got != want {
			t.Fatalf("Dbg(7, %q, \"42\") took the multi-line branch\n want %q\n  got %q",
				expr, want, got)
		}
	}
}

// TestDbg_ColourFollowsTheWriterAndNotTheEnvironment is the guard on a wrong
// colour decision, and it asserts that the check ran rather than only
// that it passed.
//
// The hazard: `dbg` is the only construct whose every line is wrapped in a
// colour decision, and a wrong decision puts escape codes in every `dbg` line
// of a captured transcript.
//
// It is one rule: `ColorEnabledFor` lives in this package and
// `internal/termcolor` calls down into it. A `bytes.Buffer` and an `exec` pipe
// are both non-terminals, so both answer false.
//
// The second half is a positive control. Without it, a Dbg that
// never coloured anything at all would satisfy the first assertion, and the
// first assertion would be telling us nothing about the decision it names.
func TestDbg_ColourFollowsTheWriterAndNotTheEnvironment(t *testing.T) {
	plain := captureStdout(t, func() { Dbg(7, "x", "42") })
	if strings.Contains(plain, "\x1b[") {
		t.Fatalf("a pipe is not a terminal, so no escape code should appear: %q", plain)
	}

	t.Setenv("NOMI_COLOR", "always")
	coloured := captureStdout(t, func() { Dbg(7, "x", "42") })
	if !strings.Contains(coloured, "\x1b[") {
		t.Fatalf("with NOMI_COLOR=always the header must be coloured; without that "+
			"the assertion above is measuring the absence of any colouring at all "+
			"rather than the writer decision: %q", coloured)
	}
	if got := ansiRe.ReplaceAllString(coloured, ""); got != plain {
		t.Fatalf("colour changed more than the escape codes\n plain %q\n stripped %q", plain, got)
	}
}

// TestDbg_HighlightHookIsOffInAnArtifact pins what a nil Highlight hook does,
// so it is a decision on record rather than a surprise.
//
// `Highlight` needs the Nomi lexer, which rt does not link, so a host that
// installs no highlighter leaves it nil and the operand text and value print
// plain even when colour is on. `nomi run` and `nomi test` install the real
// highlighter (internal/termcolor's init) and colour them. That is the
// asymmetry `Highlight`'s own comment describes for an assertion failure's
// source lines, applied to `dbg`. Golden records run with colour off and
// cannot see it, so it has a test of its own.
func TestDbg_HighlightHookIsOffInAnArtifact(t *testing.T) {
	t.Setenv("NOMI_COLOR", "always")

	saved := Highlight
	defer func() { Highlight = saved }()

	Highlight = nil
	bare := captureStdout(t, func() { Dbg(7, "x", "42") })
	if strings.Contains(bare, "<hl>") {
		t.Fatalf("a nil Highlight hook must leave the text alone: %q", bare)
	}

	Highlight = func(w io.Writer, src string) string { return "<hl>" + src + "</hl>" }
	hooked := captureStdout(t, func() { Dbg(7, "x", "42") })
	if !strings.Contains(hooked, "<hl>x</hl>") {
		t.Fatalf("the operand text must go through Highlight: %q", hooked)
	}
	if !strings.Contains(hooked, "<hl>42</hl>") {
		t.Fatalf("the rendered VALUE must go through Highlight too: %q", hooked)
	}
}

// TestDbg_MultiLineHighlightsEachSourceLineSeparately pins the loop rather than
// the whole block.
//
// DbgText highlights `part` inside its `for` over the split lines, not the
// joined text, and the difference shows the moment the hook is not a pure
// per-line function — which the real one is not: it tokenizes, so highlighting
// a joined block and highlighting its lines are two different calls.
func TestDbg_MultiLineHighlightsEachSourceLineSeparately(t *testing.T) {
	saved := Highlight
	defer func() { Highlight = saved }()
	Highlight = func(w io.Writer, src string) string { return "[" + src + "]" }

	got := captureStdout(t, func() { Dbg(7, "a\nb", "42") })
	want := "dbg line 7:\n  [a]\n  [b]\n  = [42]\n"
	if got != want {
		t.Fatalf("each source line is highlighted on its own\n want %q\n  got %q", want, got)
	}
}
