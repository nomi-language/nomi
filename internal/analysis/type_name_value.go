package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A type name is not a value. `Int`, `Point`, `Shape`, `Display` and
// `Maybe` name types; written where a value goes (`f = Int`,
// `Iter.map(xs, String)`) or called as a function (`Int("4")`,
// `String(4)`), they have no value to give. The names that are values are
// an enum variant (`None`, `Shape.Dot`, and a positional variant such as
// `Some` or `Shape.Circle` as its constructor function), a distinct type
// that wraps a value (`Id` for `type Id Int` is its constructor,
// `(Int) -> Id`; see distinctCtorValue), a zero-sized type (`type Expired`,
// whose one value is `Expired`) and a type name where a `Type<T>` witness is
// expected (`Context.value(c, TraceId)`, checkTypeWitnessExpr), wherever
// the type is declared.
//
// A type name called with arguments is construction or unwrap, and only for
// the types that have one:
//
//   - a struct takes its record: `Point({x: 1})`;
//   - a distinct type takes the value it wraps: `Email("a@b.c")`;
//   - `Int`, `Float` and `String` unwrap a distinct that wraps exactly that
//     type: `Int(id)` for `type Id Int`.
//
// Everything else is an error here, at the name, rather than a program the
// IR builder refuses with "a type name in value position".

// typeNameRef is a name that may name a type: a TypeIdent (`Int`), or the
// last segment of a qualified one (`calendar.Date`). pos is where the
// name's symbol is recorded, and text is the name as written.
type typeNameRef struct {
	node ast.Node
	pos  Pos
	text string
}

// typeNameRefOf is the type-name reference n spells, if any.
func typeNameRefOf(n ast.Node) (typeNameRef, bool) {
	switch v := n.(type) {
	case *ast.TypeIdent:
		return typeNameRef{node: v, pos: Pos{Line: v.Line, Col: v.Col}, text: v.Name}, true
	case *ast.FieldAccess:
		if v.Field == nil {
			return typeNameRef{}, false
		}
		return typeNameRef{node: v, pos: Pos{Line: v.Field.Line, Col: v.Field.Col}, text: calleeText(v)}, true
	}
	return typeNameRef{}, false
}

// namedType is the word for what ref names when it names a type rather
// than a value ("type", "interface"), and the type. ok is false for a
// variant, a zero-sized type, a function, or a name that did not resolve.
func (c *checker) namedType(ref typeNameRef) (string, Type, bool) {
	pos := ref.pos
	sym := c.fa.References[pos]
	if sym == nil {
		sym = c.fa.Definitions[pos]
	}
	if sym == nil {
		return "", nil, false
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	switch sym.Kind {
	case SymbolStruct, SymbolEnum, SymbolTypeAlias:
		return "type", sym.Type, true
	case SymbolInterface:
		return "interface", sym.Type, true
	case SymbolType:
		if sym.Type == TypeTrue || sym.Type == TypeFalse || sym.Type == TypeUnit {
			// std's host singletons: `True` and `False`, which Bool
			// embeds, and `Unit`. Each is its own one value.
			return "", nil, false
		}
		if dt, ok := sym.Type.(*DistinctType); ok && dt.Inner == nil {
			// A zero-sized `type Expired` is its one value. A `host type`
			// (`List<T>`, `Map<K, V>`, `Bytes`) carries no inner type
			// either, but its values come from functions, so its name is
			// not one.
			if _, host := sym.Node.(*ast.ExternType); !host {
				return "", nil, false
			}
		}
		return "type", sym.Type, true
	}
	return "", nil, false
}

// rejectTypeNameValue reports a type name written where a value goes:
// `f = Int`, `Iter.map(xs, String)`, `x = Point`, `d = calendar.Date`.
func (c *checker) rejectTypeNameValue(n ast.Node, ty Type) Type {
	if IsSynthesizedLine(n.LineNum()) {
		return ty
	}
	ref, ok := typeNameRefOf(n)
	if !ok {
		return ty
	}
	what, named, ok := c.namedType(ref)
	if !ok {
		return ty
	}
	c.report(errAt(n, fmt.Sprintf("`%s` is %s %s, not a value", ref.text, articleForWord(what), what)).
		WithHint(typeNameValueHint(ref.text, what, named)))
	return nil
}

func typeNameValueHint(name, what string, ty Type) string {
	if what == "interface" {
		return fmt.Sprintf("call one of its functions, as in `%s.function(x)`", name)
	}
	switch name {
	case "String":
		return "to turn a value into a String, interpolate it (`|x| \"${x}\"`) or pass `Display.to_string`"
	case "Int", "Float", "Bool":
		return "a type is not a function; pass a function that converts, such as a lambda"
	}
	switch t := ty.(type) {
	case *StructType:
		if t.Opaque {
			return "build a value with a function its module exports"
		}
		return fmt.Sprintf("build a value with `%s{...}`", name)
	case *EnumType:
		if len(t.Variants) > 0 {
			return fmt.Sprintf("a value of `%s` is one of its variants, as in `%s.%s`", name, name, t.Variants[0].Name)
		}
	}
	return ""
}

// distinctCtorValue types a distinct type named as a value, `Id` in
// `Iter.map(ints, Id)` for `type Id Int`, as its constructor: a function of
// the one value it wraps, `(Int) -> Id`. A tuple-distinct's constructor takes
// the tuple, `((Int, String)) -> Pair`, as a positional variant over a tuple
// does: the flat `Pair(1, "x")` is a call form, not a second parameter list.
// The instantiated type is recorded on the reference (attachCallType), which
// is what the IR builder reads to build the function. A zero-sized type is
// its own value and is not a constructor; an opaque one's constructor is
// private outside its file, as a call to it is. ok is false for anything that
// is not a distinct with an inner type.
func (c *checker) distinctCtorValue(n ast.Node) (Type, bool) {
	ref, ok := typeNameRefOf(n)
	if !ok || c.fa == nil || IsSynthesizedLine(n.LineNum()) {
		return nil, false
	}
	sym := c.fa.References[ref.pos]
	if sym == nil {
		return nil, false
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym.Kind != SymbolType {
		return nil, false
	}
	dt, isDistinct := sym.Type.(*DistinctType)
	if !isDistinct || dt.Inner == nil || len(dt.TypeParams) > 0 {
		return nil, false
	}
	c.checkOpaqueConstructor(n, dt)
	ft := &FuncType{Params: []Type{dt.Inner}, Return: dt}
	c.attachCallType(n, ft)
	return ft, true
}

// checkTypeNameCall checks `T(args)` for a type name T that is neither a
// function nor a distinct type (checkDistinctCtorCall has those) nor a
// struct (checkStructCallForm): only an `Int`, `Float` or `String` unwrap
// of a distinct that wraps that type is a call. argTys are the arguments'
// types, already checked; a nil one has already drawn an error, so nothing
// more is said.
func (c *checker) checkTypeNameCall(ref typeNameRef, resolved Type, args []ast.Node, argTys []Type) Type {
	if prim, isPrim := resolved.(*PrimitiveType); isPrim && len(argTys) == 1 && unwrapCallee(ref.text) {
		if dt, ok := argTys[0].(*DistinctType); ok && dt.Inner != nil && TypesEqual(dt.Inner, prim) {
			c.checkOpaqueUnwrap(args[0], argTys[0])
			return prim
		}
	}
	for _, t := range argTys {
		if t == nil {
			return nil
		}
	}
	what, _, ok := c.namedType(ref)
	if !ok {
		what = "type"
	}
	var argTy Type
	if len(argTys) == 1 {
		argTy = argTys[0]
	}
	c.report(errAt(ref.node, fmt.Sprintf("`%s` is %s %s, not a function", ref.text, articleForWord(what), what)).
		WithHint(typeNameCallHint(ref.text, what, resolved, argTy)))
	return nil
}

// calleeNamedType is the type a call's callee names when the callee is a
// type name rather than a function: `Int` in `Int("4")`, `calendar.Date`
// in `calendar.Date(3)`. ok is false for a function, a variant, a distinct
// type (which constructs) and anything that is not a type name.
func (c *checker) calleeNamedType(callee ast.Node, calleeTy Type) (typeNameRef, Type, bool) {
	switch calleeTy.(type) {
	case *FuncType, *DistinctType:
		return typeNameRef{}, nil, false
	}
	ref, ok := typeNameRefOf(callee)
	if !ok {
		return typeNameRef{}, nil, false
	}
	if ti, isIdent := callee.(*ast.TypeIdent); isIdent {
		if resolved := c.reg.Lookup(ti.Name); resolved != nil {
			return ref, resolved, true
		}
		if st, isStruct := calleeTy.(*StructType); isStruct {
			// A struct declared in a block is not in the module's
			// registry; its name's symbol carries the type. Without this
			// arm `Cell({c: 1})` was checked as an ordinary call, which
			// left a generic struct's type arguments unsolved.
			return ref, st, true
		}
		return typeNameRef{}, nil, false
	}
	_, named, ok := c.namedType(ref)
	if !ok {
		return typeNameRef{}, nil, false
	}
	return ref, named, true
}

// checkPipedTypeNameCall is checkTypeNameCall for a pipe stage,
// `"4" |> Int()`, whose piped value is the first argument. A struct's call
// form takes the piped value as its record (checkPipedStructCallForm). A
// distinct's construction keeps its own path, so handled is false for it
// and for any callee that is not a type name.
func (c *checker) checkPipedTypeNameCall(call *ast.Call, piped ast.Node, pipedTy, calleeTy Type) (Type, bool) {
	ref, resolved, ok := c.calleeNamedType(call.Func, calleeTy)
	if !ok {
		return nil, false
	}
	if st, isStruct := resolved.(*StructType); isStruct {
		return c.checkPipedStructCallForm(call, piped, pipedTy, st), true
	}
	args := append([]ast.Node{piped}, call.Args...)
	argTys := make([]Type, len(args))
	argTys[0] = pipedTy
	for i, arg := range call.Args {
		argTys[i+1] = c.checkNode(arg)
	}
	return c.checkTypeNameCall(ref, resolved, args, argTys), true
}

// unwrapCallee reports the type names whose call unwraps a distinct: the
// closed set the IR builder lowers (irdistinct.go distinctInner).
func unwrapCallee(name string) bool {
	switch name {
	case "Int", "Float", "String":
		return true
	}
	return false
}

func typeNameCallHint(name, what string, resolved, argTy Type) string {
	from := ""
	if argTy != nil {
		from = formatTypeForError(argTy)
	}
	switch name {
	case "Int":
		switch from {
		case "String":
			return "to parse a String, call `String.to_int`, which returns a `Maybe<Int>`"
		case "Float":
			return "to convert a Float, call `Float.to_int`, which returns a `Maybe<Int>`, or `Float.round` first"
		case "Decimal":
			return "to convert a Decimal, call `Decimal.to_int`, which returns a `Maybe<Int>`"
		}
	case "Float":
		switch from {
		case "Int":
			return "to convert an Int, call `Int.to_float`"
		case "Decimal":
			return "to convert a Decimal, call `Decimal.to_float`"
		}
	case "String":
		return "to turn a value into a String, interpolate it (`\"${x}\"`) or call `Display.to_string(x)`"
	}
	if unwrapCallee(name) {
		return fmt.Sprintf("`%s(x)` only unwraps a distinct type that wraps %s, as in `%s(id)` for `type Id %s`", name, name, name, name)
	}
	if what == "interface" {
		return fmt.Sprintf("call one of its functions, as in `%s.function(x)`", name)
	}
	if et, ok := resolved.(*EnumType); ok && len(et.Variants) > 0 {
		return fmt.Sprintf("build a value with one of its variants, as in `%s.%s`", name, et.Variants[0].Name)
	}
	return ""
}

func articleForWord(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}
