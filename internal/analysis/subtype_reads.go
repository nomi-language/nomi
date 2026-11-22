package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Reading a subtype (spec §4 *Subtypes*). A subtype has no impl of an
// interface that makes a value of its type (subtype_impls.go), save one that
// reads a subtype: its base's impl reads the value and the subtype's rules
// check it, a value that breaks one being the read's failure. std/json's
// FromJson is the one: `T.from_json(json)` at a Port is Int's `from_json`
// and then `Port.try_from` of the Int, a breach the ShapeError
// `expected Port, got 0`, and `T.from_missing()` is Int's, a breach None.
// So `Json.decode_as<Port>`, an anonymous struct's field of type Port, and a
// derived FromJson's field of type `List<Port>` each read Ints and check
// them. A record read from texts (std/parse's ParseFields) reads each field
// with its type's Parse, and a field of a subtype with its base's, which the
// reader checks the same way, a breach the record's None
// (structuralDemands).
//
// The check is recorded with no position: the IR builder asks for it where
// it lowers a call of a reader's function at a subtype, in whatever body,
// at the types the call is for there (CalleeAt, InterfaceCallee), and for a
// record's field at the field's type (SubtypeChecks). An instance whose
// type parameter reads a subtype is an instance of its own
// (ReadsSubtype), so the check is never shared with the instance at the
// base. A generic type's impl is lowered once for its receiver's kind, so a
// `where` of a program's impl, or of a std module's other than the
// interface's own, reads no subtype (blockReadsSubtype).

// subtypeReaders are the interfaces that read a subtype at its base.
var subtypeReaders = []StdType{stdFromJsonType}

// stdJsonShapeErrorType is std/json's ShapeError, a FromJson read's failure.
var stdJsonShapeErrorType = StdType{Module: "std/json", Name: "Json.ShapeError"}

// interfaceReadsSubtype reports whether iface reads a subtype at its base
// (subtypeReaders).
func interfaceReadsSubtype(iface *InterfaceType) bool {
	for _, s := range subtypeReaders {
		if isStdType(iface, s) {
			return true
		}
	}
	return false
}

// blockReadsSubtype reports whether the impl block info, at a type argument
// that reads a subtype under bound (ReadsSubtype), reads it: a block of
// bound's own std module (std/json's FromJson for List<T>, Maybe<T>,
// Map<String, V> and the rest), whose functions std instantiates for each
// instance's types. Any other block is lowered once for its receiver's
// kind, which would read the subtype as its base.
func blockReadsSubtype(info *ImplTypeArgs, bound *InterfaceType) bool {
	return info != nil && bound != nil && IsStdlibKey(info.File) && info.File == bound.Origin
}

// subtypeReadByKindHelp is the help for concrete, which holds a subtype, where
// bound, an interface that reads a subtype, is not met only because an impl
// whose `where` reads the subtype is one a generic type's instances share
// (blockReadsSubtype): read the type with its subtypes erased and check
// the value. ok is false for any other unmet bound.
func (c *checker) subtypeReadByKindHelp(concrete Type, bound *InterfaceType) (string, bool) {
	if bound == nil || !interfaceReadsSubtype(bound) || !HoldsSubtype(concrete) {
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

// tryFromText names the check of the subtype t a help points to: its
// `try_from`, or for an intersection the `try_from` of each member.
func tryFromText(t Type) string {
	dt := refinementOf(t)
	if dt != nil && dt.Intersection {
		return "the `try_from` of each of " + refinementParentsText(dt)
	}
	return fmt.Sprintf("`%s.try_from(...)`", typeName(t))
}

// SubtypeBreach is what a read of a subtype answers for a value that breaks
// one of its rules.
type SubtypeBreach uint8

const (
	// BreachNone is a `Maybe<self>`'s None.
	BreachNone SubtypeBreach = iota + 1
	// BreachShapeError is a `Result<self, Json.ShapeError>`'s Err of the
	// ShapeError expecting the subtype (SubtypeRead.Expected), got the
	// value's Debug text, with an empty path.
	BreachShapeError
)

// A SubtypeRead is how a call of a reader's function at a subtype checks
// the value its base's impl answers.
type SubtypeRead struct {
	// Type is the subtype.
	Type Type
	// Expected is the subtype as a ShapeError names it.
	Expected string
	// Checks are the calls that decide it (SubtypeChecks).
	Checks []SubtypeCheck
	// Breach is what the call answers for a value a check refuses.
	Breach SubtypeBreach
}

// SubtypeReadAt is the read c, a call's callee, makes when it calls a
// reader's function that answers a value of its type (`from_json`,
// `from_missing`) at a subtype, its For, which reaches the base's impl. Nil
// for any other call, and where the subtype's checks cannot be named.
func (fa *FileAnalysis) SubtypeReadAt(c Callee) *SubtypeRead {
	if fa == nil || c.For == nil {
		return nil
	}
	if dt := refinementOf(c.For); dt == nil || dt.Inner == nil {
		return nil
	}
	iface := fa.calleeInterface(c)
	if iface == nil || !interfaceReadsSubtype(iface) {
		return nil
	}
	var breach SubtypeBreach
	for _, m := range iface.Methods {
		if m.Name == c.Method {
			breach = subtypeBreachOf(m.Return, iface.SelfParam)
		}
	}
	if breach == 0 {
		return nil
	}
	checks := fa.SubtypeChecks(c.For)
	if checks == nil {
		return nil
	}
	return &SubtypeRead{Type: c.For, Expected: formatTypeForError(c.For), Checks: checks, Breach: breach}
}

// subtypeBreachOf is the breach of a function whose result is ret, self
// the interface's implementing type: BreachNone for `Maybe<self>`,
// BreachShapeError for `Result<self, Json.ShapeError>`, and zero for a
// result that makes no value of self or has no failure to report a breach
// in.
func subtypeBreachOf(ret Type, self *TypeParam_) SubtypeBreach {
	e, ok := resolveTypeVar(ret).(*EnumType)
	if !ok || self == nil || len(e.TypeArgs) == 0 || resolveTypeVar(e.TypeArgs[0]) != Type(self) {
		return 0
	}
	switch {
	case isStdType(e, stdMaybeType):
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

// A SubtypeCheck is one call that decides whether a value is a subtype:
// `S.try_from(v)`, S the subtype Type, v a value of its base.
type SubtypeCheck struct {
	Type Type
	Call Callee
}

// SubtypeChecks are the calls that decide whether a value of t's base is a
// t, t a subtype: `S.try_from(v)` with v of the base, at S's type
// arguments, for S t itself, whose `try_from` checks every rule of its
// ancestors and its own, or for an intersection each member. The value is
// a t when each answers Some. Nil when t is no subtype, or a `try_from`
// the program does not reach.
func (fa *FileAnalysis) SubtypeChecks(t Type) []SubtypeCheck {
	dt := refinementOf(t)
	if fa == nil || dt == nil || dt.Inner == nil {
		return nil
	}
	if dt.Intersection {
		var out []SubtypeCheck
		for _, m := range dt.TypeArgs {
			checks := fa.SubtypeChecks(m)
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
	return []SubtypeCheck{{Type: dt, Call: c}}
}

// tryFromCallee is the callee of `S.try_from(v)` for the subtype dt, at its
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
