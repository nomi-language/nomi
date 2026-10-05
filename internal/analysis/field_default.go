package analysis

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// Struct field default initialisers: the fifth of the five paths by which a
// value is admitted into a struct field.
//
// The builder walks the default expression for scoping (defineStruct,
// defineEnum) and records `HasDefault` (buildStructType). This file compares
// its type to the field's declared type. Without that comparison, two
// programs would analyze clean and misbehave:
//
//	struct Cfg { port: Int = "not an int" }     // would print `not an int`
//	struct Cfg { clock: Clock = NoClock{t: 7} } // would trap at run time
//	                                            // with `Clock.at: no implementation
//	                                            // for type 'NoClock'`
//
// The rule is not a separate implementation of field admission. It calls
// `argMatchesParam`, which is the same entry point struct literals and the
// constructor-call record forms use for a field (checkStructLitAgainstStruct,
// checkAnonStructTypeAgainstStruct) and which reaches `ifaceParamAdmits` for an
// interface-typed field, so interface admission has one implementation rather
// than one per syntactic form.
//
// WORDING. A bad field value has one phrasing, the one attached to
// `argMatchesParam`:
//
//	field 'clock' of Cfg: expected Clock, got NoClock
//
// It covers scalar mismatches such as `port: Int = "not an int"` as well as
// interface ones.
//
// GENERIC FIELDS. A default for a field whose declared type is a bare type
// parameter needs no special arm: it falls out of `argMatchesParam` correctly.
// `struct Box<T> { v: T = 0 }` is REFUSED, because `Box<String>{}` would put an
// Int in a String field, and `TypesEqual(Int, T)` is false with nothing to
// unify (Int carries no type variable). A default whose own type IS the
// parameter is accepted — `struct Box<T> where T: Zeroed { v: T = T.zero() }` —
// as is one that solves through the parameter,
// `struct Box<T> { items: List<T> = [] }`, where the empty list's element type
// variable binds to `T`. Both directions are pinned in field_default_test.go. This is the answer AppStructLanding asked
// for: internal/irbuild's generic-std-struct field-default channel
// states a default in the IR builder's own vocabulary and has no type parameter to
// substitute into, so a default mentioning `T` would break that property and
// nothing needs one.

// checkStructDefFieldDefaults type-checks every default initialiser on a
// struct declaration against the field's declared type.
//
// The declared types come from the struct SYMBOL rather than from the type
// registry, because the symbol is keyed by declaration position and so resolves
// for a nested or block-local struct as well as a module-level one — the same
// position-keyed lookup buildStructType itself uses when it attaches the type.
func (c *checker) checkStructDefFieldDefaults(n *ast.StructDef) {
	if n == nil || c.fa == nil {
		return
	}
	st, ok := c.declaredStructType(Pos{Line: n.Line, Col: n.Col}, n.Name).(*StructType)
	if !ok || st == nil {
		return
	}
	c.checkFieldDefaults(n.Name, n.Fields, st.Fields)
}

// checkEnumDefFieldDefaults is the enum-variant twin. A record variant's fields
// are the same `ast.StructField` values a struct's are, carry the same
// `Default`, and were unchecked for the same reason.
//
// It resolves each variant's declared field types the same way — off the enum
// symbol's own `*EnumType` — so a variant field default is checked against the
// type the variant declares rather than against nothing.
func (c *checker) checkEnumDefFieldDefaults(n *ast.EnumDef) {
	if n == nil || c.fa == nil {
		return
	}
	et, ok := c.declaredStructType(Pos{Line: n.Line, Col: n.Col}, n.Name).(*EnumType)
	if !ok || et == nil {
		return
	}
	for _, v := range n.Variants {
		var declared []FieldDef
		for _, vd := range et.Variants {
			if vd.Name == v.Name {
				declared = vd.Fields
				break
			}
		}
		if declared == nil {
			continue
		}
		c.checkFieldDefaults(n.Name+"."+v.Name, v.Fields, declared)
	}
}

// declaredStructType returns the resolved type a type declaration's symbol
// carries, preferring the position-keyed definition (which covers nested and
// block-local declarations) and falling back to module scope.
func (c *checker) declaredStructType(pos Pos, name string) Type {
	sym := c.fa.Definitions[pos]
	if sym == nil && c.fa.ModuleScope != nil {
		sym = c.fa.ModuleScope.Lookup(name)
	}
	if sym == nil {
		return nil
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	return sym.Type
}

// checkFieldDefaults is the rule itself, shared by the struct and enum-variant
// entry points. `owner` names the declaration in the diagnostic (`Cfg`, or
// `Status.Active` for a record variant).
func (c *checker) checkFieldDefaults(owner string, fields []ast.StructField, declaredFields []FieldDef) {
	for _, f := range fields {
		if f.Default == nil {
			continue
		}
		line, col := nodeLineCol(f.Default)
		if line == 0 {
			line, col = f.Line, f.Col
		}
		declared := findFieldType(declaredFields, f.Name)
		if declared == nil {
			// The annotation itself failed to resolve, or the field has
			// none. Either way the failure is already reported by
			// buildStructType and there is nothing to compare against.
			// Still check the expression so its own errors surface.
			c.checkNode(f.Default)
			continue
		}
		// Pushing the declared type as the expected type is LOAD-BEARING,
		// not cosmetic. A dot-variant shorthand has no type name to resolve
		// against, and an unsolved constructor takes its type arguments from
		// the field:
		//
		//	restart: Restart = .Temporary           // no enum named anywhere
		//	tasks: Map<Int, Task> = Map.empty()     // element types from the field
		//
		// A mutant using plain `checkNode` here leaves the first reporting
		// `.Temporary requires a determinable enum type at this position`.
		// Mirrors checkStructLit and checkStructLitAgainstStruct, which
		// push the declared field type for the same reason.
		valTy := c.checkNodeExpecting(f.Default, declared)
		if valTy == nil {
			continue
		}
		if c.argMatchesParam(valTy, declared, c.recPos(line, col), RecordingKindInterfaceTypedParam) {
			continue
		}
		c.addError(line, col, c.typef(
			"field '%s' of %s: expected %s, got %s",
			f.Name, owner, declared, valTy))
	}
}
