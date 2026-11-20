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
// structurally, as it does for a type declared in a body, which no impl
// can name: the builder writes that type's universal Debug itself
// (blocklocaltype.go), so no record names it.
type debugBody struct {
	sym        *ir.Symbol
	synth      bool
	structural bool
}

// recordedDebugBody is the Debug body the checker's record of
// `Debug.inspect(x)` at kt, a nested part's checker type, names for a part
// of kind k (recordedIfaceFnAt): a program's impl function, the instance
// at k of std's generic impl, or std's own function. No symbol where the
// record names none the renderer can call.
func (bl *irScalarBuilder) recordedDebugBody(k kind, kt analysis.Type) debugBody {
	r := bl.recordedIfaceFnAt(stdDebug, "inspect", k, kt, []kind{k}, kindString)
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
		return debugBody{sym: bl.stdInstDebugSymOf(r.template, k, kt)}
	case r.std != nil:
		if p := bl.stdDebugPlanOf(r.std, k); p != nil {
			return debugBody{sym: p.sym}
		}
	}
	return debugBody{}
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
	// debugImplBodyLocal: by the part's kind, a type declared in a body,
	// whose universal Debug the builder writes and no record names.
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
