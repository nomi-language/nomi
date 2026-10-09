package ir_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// encodedFields is every field of every type encode.go writes, as encode.go
// writes it. A round trip cannot notice a field the encoder never learned
// about: the decoder leaves it zero and re-encoding skips it again, so the
// bytes still agree. This pin is what notices: add a field to any of these
// types and this test fails until encode.go and decode.go carry it, and
// FormatVersion moves.
//
// Module.lint is the one field deliberately not encoded: it is
// LintModuleAdded's progress, and a decoded module starts it over.
var encodedFields = map[reflect.Type]string{
	reflect.TypeFor[ir.Image]():         "Modules Entry HostKeys HasMain Root",
	reflect.TypeFor[ir.Pos]():           "file line col endLine endCol synth",
	reflect.TypeFor[ir.Symbol]():        "name",
	reflect.TypeFor[ir.Block]():         "pos id label instrs term owner fault faultPos hasFault",
	reflect.TypeFor[ir.Region]():        "pos label blocks owner",
	reflect.TypeFor[ir.Func]():          "Region name sym next defs params types",
	reflect.TypeFor[ir.Param]():         "Sym Temp Shape",
	reflect.TypeFor[ir.Module]():        "name funcs cells tests boot mainFailure testBoots impls displays rowDebugs equates hashes lint",
	reflect.TypeFor[ir.Cell]():          "owner sym ty initializer",
	reflect.TypeFor[ir.TestCase]():      "name fn group",
	reflect.TypeFor[ir.TestGroup]():     "Boot Startup VirtualClock",
	reflect.TypeFor[ir.Impl]():          "Method Type Func",
	reflect.TypeFor[ir.DisplayImpl]():   "Type Func",
	reflect.TypeFor[ir.Type]():          "owner name form embeds conforms val",
	reflect.TypeFor[ir.Table]():         "types decls syms",
	reflect.TypeFor[ir.ValType]():       "kind sym elems names result layout",
	reflect.TypeFor[ir.Layout]():        "Fields Variants",
	reflect.TypeFor[ir.Variant]():       "Name Form Fields",
	reflect.TypeFor[ir.Field]():         "Name Type",
	reflect.TypeFor[ir.Jump]():          "pos target",
	reflect.TypeFor[ir.Branch]():        "pos cond ifTrue ifFalse",
	reflect.TypeFor[ir.Return]():        "pos val hasVal ctl",
	reflect.TypeFor[ir.Arith]():         "pos dst lhs rhs op dom over",
	reflect.TypeFor[ir.Call]():          "pos dst form callee fn keyAt args tail crosses",
	reflect.TypeFor[ir.Concat]():        "pos dst parts",
	reflect.TypeFor[ir.Defer]():         "pos id call",
	reflect.TypeFor[ir.RunDefer]():      "pos id",
	reflect.TypeFor[ir.FuncValue]():     "pos dst body self captures",
	reflect.TypeFor[ir.Assert]():        "pos dst subj kw text mismatch binding answer",
	reflect.TypeFor[ir.AssertBinding](): "Name Expr Val Stages",
	reflect.TypeFor[ir.AssertStage]():   "Text Val",
	reflect.TypeFor[ir.Record]():        "pos val kind text suppress",
	reflect.TypeFor[ir.Iter]():          "pos dst op over sig args",
	reflect.TypeFor[ir.Compare]():       "pos dst op shape lhs rhs rank",
	reflect.TypeFor[ir.Const]():         "pos dst kind num flt text typ",
	reflect.TypeFor[ir.Bind]():          "pos dst src sym",
	reflect.TypeFor[ir.Match]():         "pos dst subj arg kind sym text idx embeds",
	reflect.TypeFor[ir.NoMatch]():       "pos key",
	reflect.TypeFor[ir.Todo]():          "pos dst reason",
	reflect.TypeFor[ir.Not]():           "pos dst val",
	reflect.TypeFor[ir.Proj]():          "pos dst subj kind sym text idx faults shape field embeds",
	reflect.TypeFor[ir.Make]():          "pos dst kind typ text names ops tail incl embeds",
	reflect.TypeFor[ir.Slot]():          "pos slot ty",
	reflect.TypeFor[ir.Store]():         "pos kind sym src",
	reflect.TypeFor[ir.Ref]():           "pos dst kind sym",
	reflect.TypeFor[ir.Copy]():          "pos dst src",
	reflect.TypeFor[ir.Render]():        "pos dst src kind impls erased",
	reflect.TypeFor[ir.DebugImpl]():     "Type Inst Fn",
	reflect.TypeFor[ir.Try]():           "pos src text",
}

func TestImage_EveryNodeFieldIsEncoded(t *testing.T) {
	for rt, want := range encodedFields {
		var got []string
		for i := 0; i < rt.NumField(); i++ {
			got = append(got, rt.Field(i).Name)
		}
		if strings.Join(got, " ") != want {
			t.Errorf("ir.%s has fields %q; encode.go writes %q. Teach encode.go and decode.go the "+
				"change and bump ir.FormatVersion, then update this pin", rt.Name(), strings.Join(got, " "), want)
		}
	}
}
