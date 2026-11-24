package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// debugBody is a Debug body a renderer names for a nested part: its
// symbol, nil for none, and whether the front end synthesized it (a derive
// or the universal Debug). structural says the checker's record answers
// structurally, which for a declared type means a type declared in a body,
// whose Debug is its shape (nestedDebugImpls' byShape), or its file's
// bodies are not held (a file lowered from its interface), where the
// universal Debug of an opaque type declared in a body
// (analysis.FileBodies.BlockDebug) is no record's and the type's kind
// finds it (blocklocaltype.go). Where the bodies are held the record names
// that Debug, as it names a module-level type's.
type debugBody struct {
	sym        *ir.Symbol
	synth      bool
	structural bool
}

// recordedDebugBody is the Debug body the checker's record of
// `Debug.inspect(x)` at kt, a nested part's checker type, names for a part
// of kind k (recordedRenderBody).
func (bl *irScalarBuilder) recordedDebugBody(k kind, kt analysis.Type) debugBody {
	return bl.recordedRenderBody(stdDebug, "inspect", k, kt)
}

// recordedRenderBody is the body of the rendering function method of
// iface (Debug's inspect, Display's to_string) the checker's record of
// `iface.method(x)` at kt, a nested part's checker type, names for a part
// of kind k (recordedIfaceFnAt): a program's impl function, the instance
// at k of std's generic impl, or std's own function. No symbol where the
// record names none the renderer can call.
func (bl *irScalarBuilder) recordedRenderBody(iface analysis.StdType, method string, k kind, kt analysis.Type) debugBody {
	return bl.recordedRenderBodyOf(bl.g.stdIfaceCallRecord(iface, method, 1, kt).Callee, iface, method, k, kt)
}

// recordedRenderBodyOf is recordedRenderBody answered by c, the record of
// the call it means (a `dbg`'s own, dbgRecord).
func (bl *irScalarBuilder) recordedRenderBodyOf(c analysis.Callee, iface analysis.StdType, method string, k kind, kt analysis.Type) debugBody {
	r := bl.recordedIfaceFnOf(c, iface, method, k, kt, []kind{k}, kindString)
	switch {
	case r.structural:
		return debugBody{structural: true}
	case r.local != nil:
		it := r.local
		if it.impl != nil && it.impl.lowerable && it.lowerable && irImplSource(it) != nil &&
			len(it.params) == 1 && it.params[0] == k && it.result == kindString {
			return debugBody{sym: bl.g.irImplSym(it), synth: r.synth}
		}
	case r.sym != nil:
		return debugBody{sym: r.sym, synth: r.synth}
	case r.template != nil:
		return debugBody{sym: bl.stdInstSymOf(r.template, iface.Name, k, kt)}
	case r.std != nil:
		if p := bl.stdRenderPlanOf(r.std, iface, method, k); p != nil {
			return debugBody{sym: p.sym}
		}
		if f := r.std; f.canon != nil && f.canon.rtCall != "" || f.rtCall != "" {
			// std's `host fn` (Codepoint's and Date's Display) has no
			// body to name: the part renders through the thunk whose body
			// is the direct call, as a box of the type names
			// (irStdNamedBoxPlan).
			return debugBody{sym: bl.g.stdRenderThunk(iface, method, k, kt)}
		}
	}
	return debugBody{}
}

// stdRenderThunk is the thunk (stdProgThunk) whose body calls iface's
// method on a value of kind k, of checker type kt (nil where none is
// known), or nil where none is built.
func (g *gen) stdRenderThunk(iface analysis.StdType, method string, k kind, kt analysis.Type) *ir.Symbol {
	vt := g.irValType(k)
	if vt == nil || vt.Erased() {
		return nil
	}
	for i := range stdIfaceSpecs {
		if stdIfaceSpecs[i].module == iface.Module && stdIfaceSpecs[i].nomi == iface.Name {
			d := stdIfaceDefs()[i]
			if m := d.methods[method]; m != nil {
				return g.stdProgThunk(d, m, k, kt, vt.String())
			}
		}
	}
	return nil
}

// debugImplClass is how the Debug body a renderer names for a nested part
// (nestedDebugImpls) was chosen.
type debugImplClass string

const (
	// debugImplFromRecord: the part has a checker type and the record of
	// `Debug.inspect` at it names the body (recordedDebugBody).
	debugImplFromRecord debugImplClass = "from the record"
	// debugImplNoType: by the part's kind, which has no checker type
	// where the renderer names its body.
	debugImplNoType debugImplClass = "no checker type"
	// debugImplBodyLocal: by the part's kind, an opaque type declared in a
	// body whose file's bodies the record could not read
	// (debugBody.structural), whose universal Debug is written in its block
	// and no record names.
	debugImplBodyLocal debugImplClass = "a type declared in a body"
	// debugImplExistential: every implementor of the interface a held
	// value may be (existentialDebugImpls), an index query over the
	// program's impls and boxes and no use of Debug.inspect at one type.
	debugImplExistential debugImplClass = "an interface value's implementors"
	// debugImplIterSources: every source an `Iter<T>` may view (a
	// collection of T, a Range of T, a value whose type implements Iter
	// over T), which no record says the program makes.
	debugImplIterSources debugImplClass = "an Iter's sources"
)

// debugImplClasses is every debugImplClass, with whether it is the
// record's. TestIRGolden_RecordsMatchTheVM counts the distinct sites of
// each class that is not, and maxDebugImplSites holds each count to a
// maximum that only goes down.
var debugImplClasses = map[debugImplClass]bool{
	debugImplFromRecord:  true,
	debugImplNoType:      false,
	debugImplBodyLocal:   false,
	debugImplExistential: false,
	debugImplIterSources: false,
}

// noteDebugImpl counts, under substVerify, the site at (the `dbg`,
// `Debug.inspect` or `io.debug` whose renderer names the body) of a
// nested part's Debug body in the Debug body census, by how it was chosen
// (class; "" for by whether the part has a checker type kt). at is nil for
// a part an interface's implementors are walked for, which is counted at
// the site whose walk reached the interface value. A class
// debugImplClasses does not list fails the lowering.
func (bl *irScalarBuilder) noteDebugImpl(at ast.Node, class debugImplClass, kt analysis.Type) {
	g := bl.g
	if !substVerify || at == nil || g.srcFA() == nil {
		return
	}
	line, col := nodePos(at)
	if class == "" {
		class = debugImplNoType
		if kt != nil {
			class = debugImplFromRecord
		}
	}
	if _, listed := debugImplClasses[class]; !listed {
		g.calleeMismatch(line, col, fmt.Sprintf("a nested Debug body is chosen %q, which no class lists", class))
		return
	}
	debugImplCensus.note(g, string(class), line, col)
}
