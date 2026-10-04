package vm_test

// `dbg` IN THE VM: THE FOUR OBSERVABLE SHAPES, THE COLOUR RULE, AND THE PLANT
// THAT MAKES THE RECORD COMPARISON WORTH ANYTHING.
//
// Five of the nine records this engine is compared against are `dbg`
// transcripts, so the record comparison IS the acceptance for the layout, but
// only for the shape those five reach. The other shapes are pinned here.
//
// # THE FOUR SHAPES AND WHAT TRIGGERS EACH, READ OFF `rt/dbg.go`
//
//	1  EMPTY OPERAND TEXT     `dbg line 7: = 42`
//	   `expr == ""` after TrimSpace. NOT REACHABLE FROM SOURCE:
//	   `format.RenderNode` of a real AST node is never empty, and rt/dbg.go
//	   says so at the arm.
//
//	2  SINGLE-LINE            `dbg line 7: 41 + 1 = 42`
//	   `!strings.Contains(expr, "\n")`. THE ONLY SHAPE A RETAINED BODY
//	   REACHES, and that is a fact about the producer rather than about the
//	   layout: `bl.lower` requires every node of a statement to carry that
//	   statement's line, so an operand whose source text spans lines declines
//	   before `bl.dbg` is entered. All five compared records are this shape.
//
//	3  MULTI-LINE             header alone, then each source line indented
//	                          two spaces, then `  = <rendered>`
//	   the default arm. Reachable from source and NOT from a retained body,
//	   which is `testdata/nomatch`'s situation exactly — so it is pinned here
//	   over a hand-built graph, for that file's stated reason: "No record can
//	   cover a graph that no lowering produces."
//
//	4  NEWLINE-PADDED, WHICH COLLAPSES INTO 2
//	   `TrimSpace` RUNS BEFORE the newline test. An operand text of
//	   `"\n41 + 1\n"` is multi-line before trimming and single-line after, so
//	   it takes shape 2. Reversing the two statements produces a FOURTH
//	   distinct output — the multi-line layout with an empty indented source
//	   line — which is why the order is a shape and not tidiness.
//	   rt/dbg_test.go pins it for `rt.DbgText`; this pins that the VM reaches the
//	   same ordering, which it does by calling the same function.
//
// # THE COLOUR RULE, AND WHY IT IS THE ONE MOST LIKELY TO DIVERGE SILENTLY
//
// Colour is a function of the WRITER: `rt.ColorEnabledFor` ends in
// `w.(*os.File)` plus a character-device test. A test writer is a
// `strings.Builder` and a harness writer is a pipe, so BOTH answer false and
// every fixture in this repository would pass for an engine that made the
// decision about the wrong writer.
//
// And this engine has the wrong writer ready to hand. `Machine.out` is a
// `*syncWriter`, which is not an `*os.File` whatever it wraps — so a machine
// over `os.Stdout` attached to a terminal would print `dbg` UNCOLOURED where
// `nomi run` colours it, and nothing in the five populations could see it.
// `TestVMDbg_ColourFollowsTheDestinationAndNotTheWrapper` is the reading, with
// `/dev/null` as a character device that exists without a terminal.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// dbgGraph is a module holding one `fn shape()` that performs one `dbg` over a
// given source text and a given operand, and answers the operand.
//
// A HAND-BUILT GRAPH, for `nomatch_test.go`'s reason: two of the four shapes
// are not reachable from a retained body — the empty one is not reachable from
// source at all, and a multi-line operand declines at `bl.lower`'s
// same-line rule — so no lowering produces this program and no record can
// cover it. `ir.Lint` is run over it below, so it is a legal graph rather than
// whatever makes the test pass.
//
// THE FOUR NODES ARE THE FOUR `irdbg.go` BUILDS, in that order: the operand,
// the source text as an `ir.Const` String, the `ir.Render` at `RenderDebug`,
// and the marked crossing over both. The `ir.Call`'s destination is read by
// nothing, which is what makes `dbg` transparent on this side too.
func dbgGraph(t *testing.T, line int, srcText string, operand func(ir.Pos, ir.Temp) ir.Instr) *ir.Module {
	t.Helper()
	pos := ir.At("dbgshape.nomi", line, 3)
	f := ir.NewFunc(pos, "shape")
	b := f.NewBlock(pos, "entry")

	val := f.NewTemp()
	b.Append(operand(pos, val))

	txt := f.NewTemp()
	b.Append(ir.NewString(pos, txt, srcText))

	rendered := f.NewTemp()
	b.Append(ir.NewRenderDebug(pos, rendered, val))

	call := ir.NewHostCall(pos, f.NewTemp(), ir.OrdinaryCall,
		ir.NewSymbol("dbg"), txt, rendered)
	f.SetType(call.Dst(), ir.UnitType)
	b.Append(call)
	b.SetTerm(ir.NewReturn(pos, val))

	mod := ir.NewModule("dbgshape")
	mod.AddFunc(f)
	if err := ir.LintModule(mod); err != nil {
		t.Fatalf("the hand-built graph is not a legal one, so this test would be "+
			"measuring an illegal input: %v", err)
	}
	return mod
}

// dbgRun runs one hand-built shape and answers what the machine wrote.
func dbgRun(t *testing.T, line int, srcText string, operand func(ir.Pos, ir.Temp) ir.Instr) string {
	t.Helper()
	var out strings.Builder
	if _, err := vm.New(dbgGraph(t, line, srcText, operand), &out).Run("shape"); err != nil {
		t.Fatalf("running the shape: %v", err)
	}
	return out.String()
}

func dbgInt(v int64) func(ir.Pos, ir.Temp) ir.Instr {
	return func(pos ir.Pos, dst ir.Temp) ir.Instr { return ir.NewInt(pos, dst, v) }
}

// TestVMDbg_TheFourShapes pins every layout arm with a LITERAL, and the
// literals are `rt.DbgText`'s layout written out, not captured from this
// machine: a literal captured from the code under test is a record of what it
// does, not of what it must do. Shapes 2 and 4 are additionally covered by the
// five compared tour records, which is the stronger check for them; shapes 1
// and 3 have no record and could have none.
//
// THE OPERAND ORDER IS WHAT THIS ACTUALLY PROTECTS. `rt.DbgText(w, line, expr,
// rendered)` takes the source text and the rendering as two adjacent strings,
// and the producer supplies them as operands 0 and 1 of one call. Swapping
// them is a silent wrong output — `dbg line 7: 42 = a` — that compiles and
// runs.
func TestVMDbg_TheFourShapes(t *testing.T) {
	for _, tc := range []struct{ name, srcText, want string }{
		{
			name:    "1 EMPTY operand text",
			srcText: "",
			want:    "dbg line 7: = 42\n",
		},
		{
			name:    "2 SINGLE-LINE operand text",
			srcText: "41 + 1",
			want:    "dbg line 7: 41 + 1 = 42\n",
		},
		{
			name:    "3 MULTI-LINE operand text",
			srcText: "Rev{\n  zeta: 1,\n}",
			want:    "dbg line 7:\n  Rev{\n    zeta: 1,\n  }\n  = 42\n",
		},
		{
			name:    "4 NEWLINE-PADDED, collapsing into 2 because TrimSpace runs first",
			srcText: "\n41 + 1\n",
			want:    "dbg line 7: 41 + 1 = 42\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dbgRun(t, 7, tc.srcText, dbgInt(42)); got != tc.want {
				t.Fatalf("\n want %q\n  got %q", tc.want, got)
			}
		})
	}
}

// TestVMDbg_TheLayoutIsRtsAndNotASecondCopy is the claim `dbg.go`'s header
// makes, checked rather than asserted.
//
// The VM calls `rt.DbgText`, so the two agree by construction — and this is
// what fails if somebody transcribes the layout into `internal/vm` later. It
// compares the machine's bytes against `rt.DbgText` written to a captured
// stdout over the same inputs, which is the only comparison that can tell one implementation
// from two that agree today.
func TestVMDbg_TheLayoutIsRtsAndNotASecondCopy(t *testing.T) {
	for _, srcText := range []string{"", "41 + 1", "Rev{\n  zeta: 1,\n}", "\n x \n"} {
		fromVM := dbgRun(t, 7, srcText, dbgInt(42))
		fromRT := captureRtDbg(t, func() { fmt.Fprint(os.Stdout, rt.DbgText(os.Stdout, 7, srcText, "42")) })
		if fromVM != fromRT {
			t.Fatalf("the VM and rt.DbgText disagree for operand text %q\n rt %q\n vm %q",
				srcText, fromRT, fromVM)
		}
	}
}

// captureRtDbg runs f with os.Stdout replaced by a pipe and answers what it
// wrote. The colour decision is asked of os.Stdout, so observing it needs the
// fd swapped; a PIPE and not a temporary file, because the colour decision reads
// `Mode()&os.ModeCharDevice` and a pipe is what a harnessed run gets.
// rt/dbg_test.go's own helper, copied because it is
// unexported there.
func captureRtDbg(t *testing.T, f func()) string {
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

// TestVMDbg_TheDebugRenderingMatchesStdDebug pins the four operand
// kinds `bl.dbg` admits against literal Debug renderings.
//
// THE STRING ROW IS THE ONE THAT DISCRIMINATES. std's `impl Debug for String`
// escapes a backslash and a double quote AND NOTHING ELSE — a tab and a
// newline pass through raw — so `strconv.Quote` is wrong here and so is
// `rt.RowText` (the assertion row), which quotes without escaping at all. The
// literal below is the Debug rendering of
// `"he said \"hi\" and a \\ and a\ttab"`, tab included.
func TestVMDbg_TheDebugRenderingMatchesStdDebug(t *testing.T) {
	for _, tc := range []struct {
		name    string
		operand func(ir.Pos, ir.Temp) ir.Instr
		want    string
	}{
		{"Int", dbgInt(42), "42"},
		{"Float", func(p ir.Pos, d ir.Temp) ir.Instr { return ir.NewFloat(p, d, 1.5) }, "1.5"},
		{"Bool True", func(p ir.Pos, d ir.Temp) ir.Instr { return ir.NewBool(p, d, true) }, "True"},
		{"Bool False", func(p ir.Pos, d ir.Temp) ir.Instr { return ir.NewBool(p, d, false) }, "False"},
		{"Unit", func(p ir.Pos, d ir.Temp) ir.Instr { return ir.NewUnit(p, d) }, "Unit"},
		{"String, plain", func(p ir.Pos, d ir.Temp) ir.Instr { return ir.NewString(p, d, "hi") }, `"hi"`},
		{
			name: "String, with a quote, a backslash and a TAB",
			operand: func(p ir.Pos, d ir.Temp) ir.Instr {
				return ir.NewString(p, d, "he said \"hi\" and a \\ and a\ttab")
			},
			want: `"he said \"hi\" and a \\ and a` + "\t" + `tab"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := dbgRun(t, 7, "x", tc.operand)
			want := "dbg line 7: x = " + tc.want + "\n"
			if got != want {
				t.Fatalf("\n want %q\n  got %q", want, got)
			}
		})
	}
}

// TestVMDbg_TheRowDisciplineWouldHaveBeenAWrongStringAndItIsReachable is the
// plant for the choice of DISCIPLINE, which no layout test can make.
//
// `internal/irbuild`'s `debugRendering` header records reaching for the
// assertion row's renderer as its MUTANT 1, and `internal/ir/render.go` lists
// six measured disagreements between the three disciplines. `rt.RowText` is
// the row, it is one call away in the package this engine renders with, and it AGREES with Debug on three of the four admitted kinds — so a
// test whose strings are all plain would pass for the wrong renderer.
//
// So the rival is run side by side and required to DIFFER on the one input
// that separates them. Without this the whole rendering table above is
// satisfiable by the wrong implementation.
func TestVMDbg_TheRowDisciplineWouldHaveBeenAWrongStringAndItIsReachable(t *testing.T) {
	subject := "a \\ and a \" in it"
	debug := dbgRun(t, 7, "x", func(p ir.Pos, d ir.Temp) ir.Instr {
		return ir.NewString(p, d, subject)
	})
	row := "dbg line 7: x = " + rt.RowText(subject) + "\n"
	if debug == row {
		t.Fatal("the Debug rendering and the assertion ROW rendering produced the same " +
			"string for a String holding a backslash and a quote, so this engine " +
			"cannot be shown to have chosen the right one")
	}
	// AND THE DEBUG ANSWER IS THE ESCAPED ONE, which is the half that says
	// which of the two differing answers this engine gave.
	if want := "dbg line 7: x = " + `"a \\ and a \" in it"` + "\n"; debug != want {
		t.Fatalf("\n want %q\n  got %q", want, debug)
	}
}

// TestVMDbg_ColourFollowsTheDestinationAndNotTheWrapper is the reading for the
// colour rule, and it is the one fixture-shaped test cannot give.
//
// THE HAZARD, RESTATED AS THE THING MEASURED: `rt.ColorEnabledFor` ends in
// `w.(*os.File)`, `Machine.out` is a `*syncWriter`, and every writer any
// fixture or harness in this repository hands a machine is a non-terminal. So
// "the VM never colours" and "the VM asks the wrong writer" produce identical
// output everywhere it is currently observed.
//
// `/dev/null` IS THE INSTRUMENT: it is a character device, so
// `ColorEnabledFor` answers TRUE for it and FALSE for a `*syncWriter` wrapping
// it. That is the difference the machine has to get right, and it is
// observable without a terminal.
//
// THE ENVIRONMENT IS PINNED FIRST because `ColorEnabledFor`'s writer clause is
// the LAST one it reaches: NOMI_COLOR, NO_COLOR and TERM=dumb all decide ahead
// of it, so a machine running under any of them would answer the same for both
// writers and this test would measure nothing.
func TestVMDbg_ColourFollowsTheDestinationAndNotTheWrapper(t *testing.T) {
	t.Setenv("NOMI_COLOR", "")
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("TERM", "xterm")

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no character device to read the rule against: %v", err)
	}
	defer devNull.Close()

	if !rt.ColorEnabledFor(devNull) {
		t.Fatalf("THE CONTROL FAILED: %s is not being seen as a character device, so "+
			"the two readings below cannot differ and this test proves nothing",
			os.DevNull)
	}

	// The machine's own wrapper, reached the way `dbgHost` reaches it. If the
	// binding asked about `Output()` instead of the destination, the two
	// answers below would be the same and a terminal would lose its colour.
	m := vm.New(dbgGraph(t, 7, "x", dbgInt(42)), devNull)
	if rt.ColorEnabledFor(m.Output()) {
		t.Fatal("ColorEnabledFor answered TRUE for the serialized wrapper, which it " +
			"cannot do — it asserts *os.File. The reading below is then vacuous.")
	}
	// The destination is what the binding must ask about, and it answers the
	// opposite. Read through the machine's own text so this is the binding's
	// decision and not a restatement of the rule.
	coloured := rt.DbgText(devNull, 7, "x", "42")
	plain := rt.DbgText(m.Output(), 7, "x", "42")
	if coloured == plain {
		t.Fatal("rt.DbgText answered the same text for the destination and for the " +
			"wrapper, so the writer is not what the colour decision reads")
	}
	if !strings.Contains(coloured, "\x1b[") {
		t.Fatalf("the destination is a character device so the header must be "+
			"coloured: %q", coloured)
	}
	if strings.Contains(plain, "\x1b[") {
		t.Fatalf("the wrapper is not an *os.File so nothing may be coloured: %q", plain)
	}
	// AND THE BINDING PASSES THE DESTINATION, which is the half the two
	// readings above cannot give: they are about `rt`'s rule, and this is
	// about which writer the binding handed it.
	//
	// OBSERVED THROUGH `rt.Highlight`, which `rt.DbgText` calls with the
	// writer it was given. A TEE would not work and the reason is worth
	// recording, because it is the obvious attempt: `ColorEnabledFor` asserts
	// the CONCRETE type `*os.File`, so a struct embedding one is not one and
	// a wrapper cannot forward the identity. The hook sees the writer itself,
	// which is the exact fact rather than a proxy for it.
	saved := rt.Highlight
	defer func() { rt.Highlight = saved }()
	var asked []io.Writer
	rt.Highlight = func(w io.Writer, s string) string {
		asked = append(asked, w)
		return s
	}
	if _, err := vm.New(dbgGraph(t, 7, "x", dbgInt(42)), devNull).Run("shape"); err != nil {
		t.Fatalf("running over a character device: %v", err)
	}
	if len(asked) == 0 {
		t.Fatal("rt.DbgText asked Highlight about no writer at all, so this reading " +
			"has no subject")
	}
	for _, w := range asked {
		if w == io.Writer(m.Output()) {
			t.Fatal("the binding handed rt.DbgText the SERIALIZED WRAPPER, so a " +
				"machine over a terminal prints `dbg` uncoloured where `nomi run` " +
				"colours it")
		}
		if w != io.Writer(devNull) {
			t.Fatalf("the binding handed rt.DbgText %T, which is neither the "+
				"destination nor the wrapper", w)
		}
	}
}

// TestVMDbg_TheCrossingsNameIsWhatTheProducerInterned is the check that keeps
// the two `"dbg"` constants from drifting.
//
// `internal/vm` may not import `internal/irbuild` — `TestVM_ReadsTheIRAndNothingElse`
// fails if it does — so the producer's `dbgKey` and this engine's are two
// string literals in two packages. Nothing but a real lowering can show they
// agree, and the failure mode if they do not is not subtle: every `dbg`
// reports "crosses into Go and this machine binds no implementation for it".
//
// It is asserted here rather than left to the record comparison because the
// comparison would report it as five wrong transcripts rather than as one
// wrong name.
func TestVMDbg_TheCrossingsNameIsWhatTheProducerInterned(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; lowers one program; -short")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	mod := dbgLoweredModule(t, "fn main(): Int {\n  n = 2\n  dbg n\n}\n")
	found := ""
	for _, f := range mod.Funcs() {
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				c, isCall := in.(*ir.Call)
				if isCall && c.Crosses() {
					found = c.Callee().Name()
				}
			}
		}
	}
	if found == "" {
		t.Fatal("the lowering built no marked crossing, so this test has no subject")
	}
	if found != "dbg" {
		t.Fatalf("the producer interned the crossing as %q and this engine keys its "+
			"host map on \"dbg\"", found)
	}
}

// dbgLoweredModule lowers one source and answers the module holding `main`.
func dbgLoweredModule(t *testing.T, body string) *ir.Module {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nomi.toml"),
		[]byte("[module]\nname = \"dbgprobe\"\nentry_points = [\"main\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.nomi")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := irbuild.Analyze(path)
	if err != nil {
		t.Fatalf("analyzing: %v", err)
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() == "main" {
				return m
			}
		}
	}
	t.Fatal("`main` was not retained, so this test has no subject")
	return nil
}
