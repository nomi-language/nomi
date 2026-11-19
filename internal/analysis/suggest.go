package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/suggest"
)

// Suggestions for a misspelled name: the "did you mean" an unknown-name
// diagnostic ends with, by internal/suggest's rule.

// didYouMean is the hint `did you mean 'x'?` for the one candidate close to
// name (suggest.Closest), or "" when none is, or when several are equally
// close. It goes in a diagnostic's Hints (TypeError.WithHint), not its
// message.
func didYouMean(name string, candidates []string) string {
	if best := suggest.Closest(name, candidates); best != "" {
		return "did you mean '" + best + "'?"
	}
	return ""
}

// UndefinedVariableCode tags the error for a value name bound nowhere in
// scope. Its one argument is the name.
const UndefinedVariableCode = "undefined-variable"

// undefinedVariableError is the error for a value name bound nowhere in
// scope. `true` and `false` are ordinary unbound names in Nomi; the Bool
// literals are the variants `True` and `False`, which the hint names and
// the fix writes (undefinedVariableConfusion, as for `nil`).
func undefinedVariableError(n *ast.Ident, candidates []string) TypeError {
	e := errAt(n, "undefined variable '"+n.Name+"'").coded(UndefinedVariableCode, n.Name)
	if ce, ok := undefinedVariableConfusion(e, n); ok {
		return ce
	}
	return e.WithHint(didYouMean(n.Name, candidates))
}

// valueNamesAt are the value names in scope at (line, col): functions,
// parameters and bindings declared before it, `once`s, file objects and
// variants, and the file's ScopeNames for values. extra is a scope to search first
// (an attached test's).
func (c *checker) valueNamesAt(line, col int, extra *Scope) []string {
	pos := Pos{Line: line, Col: col}
	var names []string
	add := func(scope *Scope) {
		if scope == nil {
			return
		}
		for _, sym := range scope.AllVisible() {
			switch sym.Kind {
			case SymbolParam, SymbolBinding:
				if sym.Pos.Line > 0 && posBefore(pos, sym.Pos) {
					continue
				}
			case SymbolFunction, SymbolOnce, SymbolModule, SymbolEnumVariant:
			default:
				continue
			}
			names = append(names, sym.Name)
		}
	}
	add(extra)
	if c.fa != nil {
		add(c.fa.ScopeAt(pos))
		names = append(names, c.fa.scopeNames(false)...)
	}
	return names
}

// typeNamesAt are the type and variant names in scope at (line, col), and
// every type the registry knows, with the file's ScopeNames for types.
func (c *checker) typeNamesAt(line, col int) []string {
	var names []string
	if c.fa != nil {
		if scope := c.fa.ScopeAt(Pos{Line: line, Col: col}); scope != nil {
			for _, sym := range scope.AllVisible() {
				switch sym.Kind {
				case SymbolStruct, SymbolEnum, SymbolEnumVariant, SymbolType, SymbolTypeAlias, SymbolInterface:
					names = append(names, sym.Name)
				}
			}
		}
	}
	return append(names, c.reg.Names()...)
}

// variantNames are et's variant names.
func variantNames(et *EnumType) []string {
	names := make([]string, len(et.Variants))
	for i, v := range et.Variants {
		names[i] = v.Name
	}
	return names
}

// fieldNames are the names of fields.
func fieldNames(fields []FieldDef) []string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	return names
}

// typeMemberNames are what `Type.member` can name on typeName: its variants
// when it is an enum, the functions its impl blocks declare, the public ones
// the program's other modules declare for it (std's `List.length`, as
// resolveTypeMethodSymbol reaches them), and the defaults it inherits from
// its interfaces.
func (c *checker) typeMemberNames(typeName string) []string {
	var names []string
	if et, ok := c.reg.Lookup(typeName).(*EnumType); ok && et != nil {
		names = variantNames(et)
	}
	if c.fa != nil {
		fns := c.fa.TypeDefaultFunctions(typeName)
		for name, sym := range c.fa.VisibleTypeMethods(typeName) {
			fns[name] = sym
		}
		if idx := c.fa.Program().ImplIndex; idx != nil {
			for name, sym := range idx.TypeMethods[typeName] {
				if sym != nil && sym.Public {
					fns[name] = sym
				}
			}
		}
		for name := range fns {
			names = append(names, name)
		}
	}
	return names
}

// importSuggestion is the "did you mean" for an import item the file does
// not export: its public names, when the item is a top-level name.
func importSuggestion(name string, modScope *Scope, ownerSegments []string) string {
	if len(ownerSegments) > 0 {
		return ""
	}
	return didYouMean(name, publicNames(modScope))
}

// publicNames are the names scope declares publicly.
func publicNames(scope *Scope) []string {
	if scope == nil {
		return nil
	}
	var names []string
	for name, sym := range scope.Symbols {
		if sym != nil && sym.Public {
			names = append(names, name)
		}
	}
	return names
}

// membershipNames are member names that read as a membership test in
// another language (`has_key`, `includes`), which Nomi writes `x in c`.
var membershipNames = map[string]bool{
	"has": true, "has_key": true, "contains_key": true, "has_member": true,
	"includes": true, "include": true, "member": true, "is_member": true,
	"key_exists": true, "exists": true, "has_value": true, "contains_value": true,
}

// stdCollectionName is "Map", "Set" or "List" when ty is one of std's,
// else "".
func stdCollectionName(ty Type) string {
	switch t := resolveTypeVar(ty).(type) {
	case *MapType:
		return "Map"
	case *ListType:
		return "List"
	case *PrimitiveType:
		if IsStdlibKey(t.Origin) && (t.Name_ == "Map" || t.Name_ == "Set" || t.Name_ == "List") {
			return t.Name_
		}
	case *DistinctType:
		if IsStdlibKey(t.Origin) && (t.Name == "Map" || t.Name == "Set" || t.Name == "List") {
			return t.Name
		}
	case *StructType:
		if IsStdlibKey(t.Origin) && (t.Name == "Map" || t.Name == "Set" || t.Name == "List") {
			return t.Name
		}
	}
	return ""
}

// membershipHint is the hint for a member of the collection named
// collection ("Map", "Set", "List") that it lacks and that reads as a
// membership test: Nomi tests membership with `in`, which calls the
// collection's `contains` (the `Contains` interface).
// iterMemberHint is the help for `List.sum`, `Set.map` or `Vector.get`: a
// member a std List, Set or Vector lacks that Iter has under the same name,
// and which works on it, since each is iterable. Otherwise "".
func (c *checker) iterMemberHint(ty Type, member string) string {
	name := stdCollectionName(ty)
	if name == "" {
		if t, ok := resolveTypeVar(ty).(*PrimitiveType); ok && IsStdlibKey(t.Origin) && t.Name_ == "Vector" {
			name = "Vector"
		}
	}
	if name == "" || name == "Map" {
		return ""
	}
	if sym, _ := c.resolveTypeMethodSymbol("Iter", member, ""); sym == nil {
		return ""
	}
	return fmt.Sprintf("`%s` is an `Iter` function, and works on a %s: write `Iter.%s(...)`", member, name, member)
}

func membershipHint(collection, member string) string {
	if collection == "" || !membershipNames[member] {
		return ""
	}
	switch collection {
	case "Map":
		if member == "has_value" || member == "contains_value" {
			return "test a value with `v in Map.values(m)`; `k in m` tests a key"
		}
		return "test a key with `k in m` (it calls `Map.contains(m, k)`); `Map.get(m, k)` gives its value as a `Maybe`"
	case "Set":
		return "test membership with `x in s` (it calls `Set.contains(s, x)`)"
	case "List":
		return "test membership with `x in list` (it calls `List.contains(list, x)`)"
	}
	return ""
}

// emptyCollectionLiteralHint is the hint for an empty constructor a
// collection does not have: the empty Map, Set and Vector are written as
// their literals, as the empty List is `[]`. ty is the type the call names,
// and the hint is "" for any type but std's and any other member.
func emptyCollectionLiteralHint(ty Type, member string) string {
	if member != "empty" && member != "new" {
		return ""
	}
	var name, origin string
	switch t := ty.(type) {
	case *PrimitiveType:
		name, origin = t.Name_, t.Origin
	case *DistinctType:
		name, origin = t.Name, t.Origin
	case *StructType:
		name, origin = t.Name, t.Origin
	default:
		return ""
	}
	if !IsStdlibKey(origin) {
		return ""
	}
	switch name {
	case "Map":
		return "write the empty map as {}"
	case "Set":
		return "write the empty set as Set[]"
	case "Vector":
		return "write the empty vector as Vector[]"
	}
	return ""
}
