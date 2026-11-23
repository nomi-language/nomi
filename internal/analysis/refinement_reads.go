package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Reading a refinement (spec §4 *Refinements*). A refinement has no impl of an
// interface that makes a value of its type (refinement_impls.go), save one that
// reads a refinement: its base's impl reads the value and the refinement's rules
// check it, a value that breaks one being the read's failure. std/json's
// FromJson is the one: `T.from_json(json)` at a Port is Int's `from_json`
// and then `Port.try_from` of the Int, a breach the ShapeError
// `expected Port, got 0`, and `T.from_missing()` is Int's, a breach None.
// So `Json.decode_as<Port>`, an anonymous struct's field of type Port, and a
// derived FromJson's field of type `List<Port>` each read Ints and check
// them. A record read from texts (std/parse's ParseFields) reads each field
// with its type's Parse, and a field of a refinement with its base's, which the
// reader checks the same way, a breach the record's None
// (structuralDemands).
//
// The check is recorded with no position: the IR builder asks for it where
// it lowers a call of a reader's function at a refinement, in whatever body,
// at the types the call is for there (CalleeAt, InterfaceCallee), and for a
// record's field at the field's type (RefinementCheckCalls). An instance whose
// type parameter reads a refinement is an instance of its own
// (ReadsRefinement), so the check is never shared with the instance at the
// base. A generic type's impl is lowered once for its receiver's kind, so a
// `where` of a program's impl, or of a std module's other than the
// interface's own, reads no refinement (blockReadsRefinement).

// refinementReaders are the interfaces that read a refinement at its base.
var refinementReaders = []StdType{stdFromJsonType}

// stdJsonShapeErrorType is std/json's ShapeError, a FromJson read's failure.
var stdJsonShapeErrorType = StdType{Module: "std/json", Name: "Json.ShapeError"}

// interfaceReadsRefinement reports whether iface reads a refinement at its base
// (refinementReaders).
func interfaceReadsRefinement(iface *InterfaceType) bool {
	for _, s := range refinementReaders {
		if isStdType(iface, s) {
			return true
		}
	}
	return false
}

// blockReadsRefinement reports whether the impl block info, at a type argument
// that reads a refinement under bound (ReadsRefinement), reads it: a block of
// bound's own std module (std/json's FromJson for List<T>, Option<T>,
// Map<String, V> and the rest), whose functions std instantiates for each
// instance's types. Any other block is lowered once for its receiver's
// kind, which would read the refinement as its base.
func blockReadsRefinement(info *ImplTypeArgs, bound *InterfaceType) bool {
	return info != nil && bound != nil && IsStdlibKey(info.File) && info.File == bound.Origin
}

// refinementReadByKindHelp is the help for concrete, which holds a refinement, where
// bound, an interface that reads a refinement, is not met only because an impl
// whose `where` reads the refinement is one a generic type's instances share
// (blockReadsRefinement): read the type with its refinements erased and check
// the value. ok is false for any other unmet bound.
func (c *checker) refinementReadByKindHelp(concrete Type, bound *InterfaceType) (string, bool) {
	if bound == nil || !interfaceReadsRefinement(bound) || !HoldsRefinement(concrete) {
		return "", false
	}
	erased := eraseRefinements(concrete, 0)
	if !typeSatisfiesInterfaceForImplMatch(erased, bound, c.implsContext(), c.implTypeArgsContext(), nil) {
		return "", false
	}
	var sub Type
	walkType(concrete, func(p Type) bool {
		if sub == nil && refinementOf(p) != nil {
			sub = resolveTypeVar(p)
		}
		return sub == nil
	})
	return fmt.Sprintf("%s is read through an impl of %s that one generic type's instances share, which would read %s as its base; read %s and check the value with %s",
		formatTypeForError(concrete), bound.Name, formatTypeForError(sub), formatTypeForError(erased), tryFromText(sub)), true
}

// tryFromText names the check of the refinement t a help points to: its
// `try_from`, or for an intersection the `try_from` of each member.
func tryFromText(t Type) string {
	dt := refinementOf(t)
	if dt != nil && dt.Intersection {
		return "the `try_from` of each of " + refinementParentsText(dt)
	}
	return fmt.Sprintf("`%s.try_from(...)`", typeName(t))
}

// RefinementBreach is what a read of a refinement answers for a value that breaks
// one of its rules.
type RefinementBreach uint8

const (
	// BreachNone is an `Option<self>`'s None.
	BreachNone RefinementBreach = iota + 1
	// BreachShapeError is a `Result<self, Json.ShapeError>`'s Err of the
	// ShapeError expecting the refinement (RefinementRead.Expected), got the
	// value's Debug text, with an empty path.
	BreachShapeError
)

// A RefinementRead is how a call of a reader's function at a refinement checks
// the value its base's impl answers.
type RefinementRead struct {
	// Type is the refinement.
	Type Type
	// Expected is the refinement as a ShapeError names it.
	Expected string
	// Checks are the calls that decide it (RefinementCheckCalls).
	Checks []RefinementCheck
	// Breach is what the call answers for a value a check refuses.
	Breach RefinementBreach
}

// RefinementReadAt is the read c, a call's callee, makes when it calls a
// reader's function that answers a value of its type (`from_json`,
// `from_missing`) at a refinement, its For, which reaches the base's impl. Nil
// for any other call, and where the refinement's checks cannot be named.
func (fa *FileAnalysis) RefinementReadAt(c Callee) *RefinementRead {
	if fa == nil || c.For == nil {
		return nil
	}
	if dt := refinementOf(c.For); dt == nil || dt.Inner == nil {
		return nil
	}
	iface := fa.calleeInterface(c)
	if iface == nil || !interfaceReadsRefinement(iface) {
		return nil
	}
	var breach RefinementBreach
	for _, m := range iface.Methods {
		if m.Name == c.Method {
			breach = refinementBreachOf(m.Return, iface.SelfParam)
		}
	}
	if breach == 0 {
		return nil
	}
	checks := fa.RefinementCheckCalls(c.For)
	if checks == nil {
		return nil
	}
	return &RefinementRead{Type: c.For, Expected: formatTypeForError(c.For), Checks: checks, Breach: breach}
}

// refinementBreachOf is the breach of a function whose result is ret, self
// the interface's implementing type: BreachNone for `Option<self>`,
// BreachShapeError for `Result<self, Json.ShapeError>`, and zero for a
// result that makes no value of self or has no failure to report a breach
// in.
func refinementBreachOf(ret Type, self *TypeParam_) RefinementBreach {
	e, ok := resolveTypeVar(ret).(*EnumType)
	if !ok || self == nil || len(e.TypeArgs) == 0 || resolveTypeVar(e.TypeArgs[0]) != Type(self) {
		return 0
	}
	switch {
	case isStdType(e, stdOptionType):
		return BreachNone
	case isStdType(e, stdResultType) && len(e.TypeArgs) == 2 && isStdType(resolveTypeVar(e.TypeArgs[1]), stdJsonShapeErrorType):
		return BreachShapeError
	}
	return 0
}

// calleeInterface is the interface whose function c calls: the one the
// call names, or for a call qualified by a type, the one the impl block it
// reaches implements. Nil for a call of no interface's function.
func (fa *FileAnalysis) calleeInterface(c Callee) *InterfaceType {
	for _, q := range []Qualifier{c.Owner, c.Qualifier} {
		if q.Kind == QualInterface && !q.Std.IsZero() {
			return fa.stdInterface(q.Std)
		}
	}
	if ib, ok := c.Impl.(*ast.ImplBlock); ok {
		if info := ImplBlockTypeArgs(fa.declaringFileOf(c.Decl), ib); info != nil && !info.Std().IsZero() {
			return fa.stdInterface(info.Std())
		}
	}
	return nil
}

// stdInterface is the interface s, as the std module that declares it
// binds it, or nil.
func (fa *FileAnalysis) stdInterface(s StdType) *InterfaceType {
	p := fa.Program()
	if p == nil {
		return nil
	}
	scope := p.StdlibModuleScopes[strings.TrimPrefix(s.Module, "std/")]
	if scope == nil {
		return nil
	}
	sym := scope.LookupLocal(s.Name)
	if sym == nil {
		return nil
	}
	it, _ := resolveRef(sym).Type.(*InterfaceType)
	return it
}

// A RefinementCheck is one call that decides whether a value is a refinement:
// `S.try_from(v)`, S the refinement Type, v a value of its base.
type RefinementCheck struct {
	Type Type
	Call Callee
}

// RefinementCheckCalls are the calls that decide whether a value of t's base is a
// t, t a refinement: `S.try_from(v)` with v of the base, at S's type
// arguments, for S t itself, whose `try_from` checks every rule of its
// ancestors and its own, or for an intersection each member. The value is
// a t when each answers Some. Nil when t is no refinement, or a `try_from`
// the program does not reach.
func (fa *FileAnalysis) RefinementCheckCalls(t Type) []RefinementCheck {
	dt := refinementOf(t)
	if fa == nil || dt == nil || dt.Inner == nil {
		return nil
	}
	if dt.Intersection {
		var out []RefinementCheck
		for _, m := range dt.TypeArgs {
			checks := fa.RefinementCheckCalls(m)
			if checks == nil {
				return nil
			}
			out = append(out, checks...)
		}
		return out
	}
	c, ok := fa.tryFromCallee(dt)
	if !ok {
		return nil
	}
	return []RefinementCheck{{Type: dt, Call: c}}
}

// tryFromCallee is the callee of `S.try_from(v)` for the refinement dt, at its
// type arguments: what the checker records for that call written in the
// file that declares S.
func (fa *FileAnalysis) tryFromCallee(dt *DistinctType) (Callee, bool) {
	home := fa.fileOfOrigin(dt.Origin)
	if home == nil || home.ModuleScope == nil {
		return Callee{}, false
	}
	typeSym := home.ModuleScope.LookupLocal(dt.Name)
	if typeSym == nil {
		return Callee{}, false
	}
	sym := home.LookupTypeMethod(dt.Name, "try_from")
	if sym == nil {
		return Callee{}, false
	}
	ref := sym
	if len(dt.TypeArgs) > 0 && len(dt.TypeArgs) == len(dt.TypeParamDefs) {
		byName := make(map[string]Type, len(dt.TypeArgs))
		for i, tp := range dt.TypeParamDefs {
			byName[tp.Name_] = dt.TypeArgs[i]
		}
		at := *sym
		at.CallType, _ = ReplaceTypeParams(sym.Type, func(tp *TypeParam_) (Type, bool) {
			t, ok := byName[tp.Name_]
			return t, ok
		})
		at.CallTypeArgs = byName
		ref = &at
	}
	q := home.QualifierOfSymbol(resolveRef(typeSym))
	out := Callee{Kind: CalleeFunction, Ref: ref, Decl: sym.Node, File: sym.SourceFile, Qualifier: q, Owner: q, Method: "try_from"}
	return fa.withStd(fa.withDeclaringFile(out)), true
}
