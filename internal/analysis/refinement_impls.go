package analysis

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A refinement has its base's impls (spec §4 *Refinements*), save those of an
// interface that makes a value of its implementing type.
//
// A function of an interface makes a value of its type when its result
// names `self` (`Parse.parse(text) -> Result<self, E>`, `Discrete.next(value:
// self) -> Option<self>`, `Summable.empty_sum() -> self`) and an impl writes
// its body: it has no default, or its default is a host function
// (`Struct.update`), builds `self(x)` (distinct_self.go) or names what its
// `where self` requires, whose functions may make one. The base's impl of
// such an interface makes a value of the base, which no rule of the refinement
// has checked, so a refinement does not have that impl (interfaceMakesSelf):
// `Parse.parse<Port>(text)` and `Summable.empty_sum<Port>()` are errors
// (RefinementImplMakesBaseCode), save where the interface reads a refinement
// at its base and checks it (FromJson, and a record's field read as
// ParseFields; refinement_reads.go), and a generic call whose type parameter
// the arguments solve to a refinement and whose bounds include such an
// interface is called at the base instead (widenPastMadeSelf), so
// `Iter.sum(ports)` is an Int and `Discrete.next(p)` an `Option<Int>`.
//
// Every site that answers whether a type has an impl asks refinementLacksImpl
// where it would read a refinement as its base: the checker's two
// typeImplementsInterface, typeSatisfiesInterfaceForImplMatch for an impl's
// own bounds, implCandidates, and recordConformanceRecording for a demand.
//
// A default written in the interface is generic in `self`: it can answer
// only a `self` it was given (`Comparable.max`) or one a function that
// makes one answered, so an interface whose every function naming `self`
// in its result is such a default (`Comparable`, with `min`, `max` and
// `clamp`) still serves a refinement. A base's impl that writes its own body
// for such a default (Float's `max`) is held to the default's contract.

// RefinementImplMakesBaseCode tags the error for a refinement where an impl of an
// interface that makes a value of its type is needed: the refinement has no
// such impl, and its base's makes a value of the base.
const RefinementImplMakesBaseCode = "refinement-impl-makes-base"

// interfaceMakesSelf reports whether a function of iface makes a value of
// its implementing type that an impl's own body builds.
func interfaceMakesSelf(iface *InterfaceType) bool {
	return interfaceMakesSelfSeen(iface, map[string]bool{})
}

func interfaceMakesSelfSeen(iface *InterfaceType, seen map[string]bool) bool {
	if iface == nil || iface.SelfParam == nil {
		return false
	}
	key := iface.Origin + "\x00" + iface.Name
	if seen[key] {
		return false
	}
	seen[key] = true
	for _, m := range iface.Methods {
		if _, ok := methodMakesSelf(iface, m, seen); ok {
			return true
		}
	}
	return false
}

// methodMakesSelf reports whether m, a function of iface, makes a value of
// iface's implementing type, and its name.
func methodMakesSelf(iface *InterfaceType, m MethodSig, seen map[string]bool) (string, bool) {
	if m.Return == nil || !typeMentionsParam(m.Return, iface.SelfParam) {
		return "", false
	}
	if !m.HasDefault || m.Extern || m.BuildsSelf {
		return m.Name, true
	}
	for _, b := range m.SelfBounds {
		if interfaceMakesSelfSeen(b, seen) {
			return m.Name, true
		}
	}
	return "", false
}

// selfMaker is the name of the first function of iface that makes a value of
// its implementing type, for the error that names it.
func selfMaker(iface *InterfaceType) string {
	for _, m := range iface.Methods {
		if name, ok := methodMakesSelf(iface, m, map[string]bool{}); ok {
			return name
		}
	}
	return ""
}

// refinementLacksImpl reports whether t is a refinement, at the top, that has no
// impl of iface because iface makes a value of its implementing type and
// does not read a refinement at its base (interfaceReadsRefinement).
func refinementLacksImpl(t Type, iface *InterfaceType) bool {
	dt := refinementOf(t)
	return dt != nil && dt.Inner != nil && interfaceMakesSelf(iface) && !interfaceReadsRefinement(iface)
}

// refinementLacksImplNamed is refinementLacksImpl for an interface named by name
// and origin, as the conformance sites that carry no *InterfaceType name it:
// the declaration origin's file holds (interfaceDeclaredAt), or with no
// recorded origin the one name reaches in fa's scope.
func (fa *FileAnalysis) refinementLacksImplNamed(t Type, name, origin string) bool {
	if refinementOf(t) == nil {
		return false
	}
	return refinementLacksImpl(t, fa.interfaceNamed(name, origin))
}

// interfaceNamed is the interface named name that origin declares, or with
// no recorded origin the one name reaches in fa's scope. Nil when neither
// names one.
func (fa *FileAnalysis) interfaceNamed(name, origin string) *InterfaceType {
	iface, _ := fa.interfaceDeclaredAt(origin, name)
	if iface == nil && origin == OriginUnresolved {
		iface = lookupInterfaceType(fa, nil, name)
	}
	return iface
}

// refinementMissing is the refinement in concrete that leaves it without an impl
// of bound, when concrete has none and the type with its refinements erased
// has one: `Port` for `Port` or `List<Port>` and FromJson. Nil otherwise.
func (c *checker) refinementMissing(concrete Type, bound *InterfaceType) Type {
	if bound == nil || !interfaceMakesSelf(bound) || interfaceReadsRefinement(bound) || !refinementInType(concrete) {
		return nil
	}
	if !typeSatisfiesInterfaceForImplMatch(eraseRefinements(concrete, 0), bound, c.implsContext(), c.implTypeArgsContext(), nil) {
		return nil
	}
	var sub Type
	walkType(concrete, func(p Type) bool {
		if sub == nil && refinementOf(p) != nil {
			sub = resolveTypeVar(p)
		}
		return sub == nil
	})
	return sub
}

// widenPastMadeSelf is subs with each type parameter that an argument solved
// to a refinement, and that a bound of an interface that makes a value of its
// type constrains, solved to the refinement's base instead, when every
// argument converts to the parameter's type at the base: `Iter.sum(ports)`
// sums Ints, and `Discrete.next(p)` is Int's. bounds are the call's own
// where-bounds; iface is the interface a qualified call names, whose `self`
// it bounds. A parameter a type argument names (written) keeps its refinement,
// and its bound's error names it. values pairs each parameter's declared
// type with its argument's. It is subs unchanged when nothing widens.
func (c *checker) widenPastMadeSelf(ft *FuncType, subs map[*TypeParam_]Type, written map[*TypeParam_]bool, iface *InterfaceType, values [][2]Type) map[*TypeParam_]Type {
	var trial map[*TypeParam_]Type
	for tp, t := range subs {
		if written[tp] || refinementOf(t) == nil || !paramMakesSelf(tp, ft, iface) {
			continue
		}
		if trial == nil {
			trial = make(map[*TypeParam_]Type, len(subs))
			for k, v := range subs {
				trial[k] = v
			}
		}
		trial[tp] = RefinementBase(t)
	}
	if trial == nil {
		return subs
	}
	for _, pa := range values {
		if want := Substitute(pa[0], trial); !ContainsTypeParam(want) && c.unifyInto(want, pa[1], map[*TypeParam_]Type{}) != nil {
			return subs
		}
	}
	return trial
}

// paramMakesSelf reports whether a bound of tp, its own, a where-bound of
// ft's or, for iface's `self`, iface itself, makes a value of its type.
func paramMakesSelf(tp *TypeParam_, ft *FuncType, iface *InterfaceType) bool {
	if iface != nil && iface.SelfParam == tp && interfaceMakesSelf(iface) {
		return true
	}
	for _, b := range tp.Bounds {
		if interfaceMakesSelf(b) {
			return true
		}
	}
	for _, wb := range ft.WhereBounds {
		if wb.Param != tp {
			continue
		}
		for _, b := range wb.Bounds {
			if interfaceMakesSelf(b) {
				return true
			}
		}
	}
	return false
}

// ReadsRefinement reports whether a type parameter that bounds bound, standing
// for t at an instance of a generic declaration, reads a refinement: a bound
// makes a value of its type (interfaceMakesSelf) and t is or holds a
// refinement. Such an instance makes values of the refinements t holds through
// that bound, so it is not the instance at t with its refinements erased: the
// IR builder keeps one apart for t (RefinementShape), where every other
// instance at a refinement is its base's.
func ReadsRefinement(bounds []*InterfaceType, t Type) bool {
	if !HoldsRefinement(t) {
		return false
	}
	for _, b := range bounds {
		if interfaceMakesSelf(b) {
			return true
		}
	}
	return false
}

// HoldsRefinement reports whether t is a refinement or holds one at any depth.
func HoldsRefinement(t Type) bool {
	return t != nil && refinementInType(t)
}

// RefinementShape spells where t holds its refinements: each refinement by its
// declaring file and name (an intersection by its members joined by `&`),
// followed by its type arguments, every part that is no refinement and holds
// none as `_`, and the parts of any other type in order, an anonymous
// struct's with their field names. Two types of one shape with their
// refinements erased hold the same refinements at the same places exactly when
// their spellings are equal.
func RefinementShape(t Type) string {
	var b strings.Builder
	writeRefinementShape(&b, t, 0)
	return b.String()
}

func writeRefinementShape(b *strings.Builder, t Type, depth int) {
	t = resolveTypeVar(t)
	if depth > maxTypeWalkDepth || !refinementInType(t) {
		b.WriteByte('_')
		return
	}
	kids := typeChildren(t)
	if dt := refinementOf(t); dt != nil {
		if dt.Intersection {
			for i, m := range kids {
				if i > 0 {
					b.WriteByte('&')
				}
				writeRefinementShape(b, m, depth+1)
			}
			return
		}
		b.WriteString(dt.Origin + "." + dt.Name)
		if len(kids) == 0 {
			return
		}
	}
	anon, _ := t.(*AnonStructType)
	b.WriteByte('(')
	for i, k := range kids {
		if i > 0 {
			b.WriteByte(',')
		}
		if anon != nil {
			b.WriteString(anon.Fields[i].Name + ":")
		}
		writeRefinementShape(b, k, depth+1)
	}
	b.WriteByte(')')
}

// DeclTypeParamBounds is the bounds of each type parameter of fd, a
// function fa declares, by name, as the checker typed fd: its own, which
// its signature and its `where` give it, and, for a function of impl block
// ib (nil for none), the block's, which the block's `where` gives it.
func DeclTypeParamBounds(fa *FileAnalysis, fd *ast.FuncDef, ib *ast.ImplBlock) map[string][]*InterfaceType {
	out := map[string][]*InterfaceType{}
	add := func(p *TypeParam_, bounds []*InterfaceType) {
		for _, b := range bounds {
			if b != nil && !slices.Contains(out[p.Name_], b) {
				out[p.Name_] = append(out[p.Name_], b)
			}
		}
	}
	if info := ImplBlockTypeArgs(fa, ib); info != nil {
		for _, p := range info.TypeParamDefs {
			add(p, p.Bounds)
		}
	}
	if fa == nil || fd == nil {
		return out
	}
	sym := fa.Definitions[Pos{Line: fd.Line, Col: fd.Col}]
	if sym == nil || sym.Node != fd {
		return out
	}
	ft, _ := sym.Type.(*FuncType)
	if ft == nil {
		return out
	}
	for _, t := range append(slices.Clip(ft.Params), ft.Return) {
		walkType(t, func(p Type) bool {
			if tp, ok := p.(*TypeParam_); ok {
				add(tp, tp.Bounds)
			}
			return true
		})
	}
	for _, wb := range ft.WhereBounds {
		if wb.Param != nil {
			add(wb.Param, append(slices.Clip(wb.Param.Bounds), wb.Bounds...))
		}
	}
	return out
}

// refinementMakesBaseError is the error for refinement t where an impl of iface is
// needed and iface makes a value of its implementing type. required says
// what needs it, as the neighbouring bound errors say it (`where T: Parse`,
// `Parse.parse`), and may be empty.
func refinementMakesBaseError(line, col int, t Type, iface *InterfaceType, required string) TypeError {
	base := formatTypeForError(RefinementBase(t))
	sub := formatTypeForError(t)
	need := ""
	if required != "" {
		need = " (required by " + required + ")"
	}
	maker := iface.Name
	if fn := selfMaker(iface); fn != "" {
		maker = "`" + iface.Name + "." + fn + "`"
	}
	return TypeError{Line: line, Col: col, Code: RefinementImplMakesBaseCode, Message: fmt.Sprintf(
		"%s has no impl of %s%s: %s is a refinement of %s, and %s's %s makes %s %s that %s's rule has not checked",
		sub, iface, need, sub, base, base, maker, ArticleFor(base), base, sub)}.WithHint(refinementMakesBaseHelp(t))
}

// refinementMakesBaseHelp is the fix for a refinement with no impl of an interface
// that makes a value: make the base's value and check it with try_from.
func refinementMakesBaseHelp(t Type) string {
	base := formatTypeForError(RefinementBase(t))
	dt := refinementOf(t)
	if dt.Intersection {
		// An intersection has no try_from of its own: its members do.
		return fmt.Sprintf("make %s %s and check it with the `try_from` of each of %s",
			ArticleFor(base), base, refinementParentsText(dt))
	}
	return fmt.Sprintf("make %s %s and check it with `%s.try_from(...)`, which is `Some` %s",
		ArticleFor(base), base, dt.Name, tryFromSomeWhen(dt))
}
