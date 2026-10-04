// Package ffitypes is the single encoding of the scalar Nomi <-> Go type
// projection the FFI boundary is defined by (spec: "Go binding signatures
// project Nomi ...").
//
// The table is REPRESENTATION-NEUTRAL: it is expressed in Nomi type names
// and Go SOURCE spellings, and nothing in this file mentions reflect or
// go/ast. Each consumer is a thin adapter over it:
//
//   - runtime asks whether the reflect.Type registered under a `host fn`
//     can realize the declared Nomi type, so a disagreement fails at
//     LoadSource instead of at the first call — which for a cold-path
//     extern can be never. See Match in reflectmatch.go.
//   - internal/ffirun walks a Go signature as go/ast and asks which Nomi
//     type each Go spelling projects to, so it can compare that against
//     the text of the declaration. See NomiForGoIdent.
//   - internal/ffirun also reads the table in the GENERATIVE direction: the
//     Go type the generated FFI wrapper writes for a declared Nomi type is
//     projected from the declaration. Expect returns that projection as a
//     GoType it can render directly.
//
// One table because there is one spec paragraph. A second copy keyed on
// reflect.Kind would be the same knowledge written twice, and the copies
// would drift on the first spec change.
//
// # Direction, and why it is the safe one
//
// Checking runs the projection FORWARD: from the DECLARED NOMI TYPE to the
// Go type it expects. The inverse (a Go type -> the Nomi type it must be)
// is lossy and would false-reject legal declarations: `int64` is `Int` but
// is equally every opaque distinct type over `Int`; `any` is `Dynamic` but
// is equally any handle; a Go struct is any Nomi struct with matching
// fields. A false rejection breaks a program that works, which is strictly
// worse than the mistimed error the check exists to fix.
//
// Forward, the question has a provable answer for exactly the rows below
// and no others. `String` projects to `string` and nothing else, so a
// registered `int64` is provably wrong. An opaque `Meters` over `Int` has
// more than one legal Go spelling, so Expect reports no expectation and
// every caller must skip. Conservatism is a property of the table, not of
// the caller's care.
package ffitypes

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// The Nomi type names the table covers. Exported because the go/ast side
// compares against them by name — its projection produces Nomi type text,
// not AST — and a literal there would be a second spelling of one fact.
const (
	NomiString   = "String"
	NomiBool     = "Bool"
	NomiByte     = "Byte"
	NomiInt      = "Int"
	NomiFloat    = "Float"
	NomiBytes    = "Bytes"
	NomiDuration = "Duration"
	NomiInstant  = "Instant"
	NomiDynamic  = "Dynamic"
	NomiUnit     = "Unit"
)

// The generic Nomi types whose Go projection is defined element-wise:
// `List<T>` -> `[]T`, `Maybe<T>` -> `*T`, `Map<K, V>` -> `map[K]V`. Each is
// projected only when every element position is, because `[]T` with an
// unprovable T names no Go type.
const (
	nomiList  = "List"
	nomiMaybe = "Maybe"
	nomiMap   = "Map"
)

// GoShape is the Go type constructor a projection produces.
type GoShape int

const (
	// GoNone is the zero shape: no Go type at all. A GoType with this
	// shape is the statement "this Nomi type has no unique Go
	// expectation", which is what Dynamic and Unit project to.
	GoNone GoShape = iota
	// GoNamed is a predeclared Go type (`string`, `int64`) or a named
	// stdlib one (`time.Duration`).
	GoNamed
	GoSlice
	GoPointer
	GoMap
)

// GoType is the Go type a Nomi type projects to, described structurally so
// each consumer can render it as source, compare it against a reflect.Type,
// or compare it against a go/ast expression without this package knowing
// which.
type GoType struct {
	Shape GoShape
	// Nomi is the Nomi type this node projects from. Carried so an adapter
	// holding only the descriptor can still ask RealizedBy which Go
	// spellings the ROW accepts — the canonical spelling in Name is what
	// the projection produces, not the only thing that satisfies it.
	Nomi string
	// Pkg is the import path a named stdlib type needs ("time"); empty for
	// a predeclared type.
	Pkg string
	// Name is the canonical Go spelling for GoNamed: "string", "int64",
	// "Duration". Empty for the container shapes.
	Name string
	// Key and Elem are the map key / element or pointee descriptors.
	Key  *GoType
	Elem *GoType
}

// String renders the projection as Go source under the default package
// qualifiers. Empty for GoNone, which is what makes "" a safe stand-in for
// "no expectation" in a diagnostic.
func (g GoType) String() string { return g.Render(nil) }

// Render renders the projection as Go source, asking qualifier which name a
// named type's package is spelled under. A generator that imports "time"
// under an alias needs that hook; qualifier may be nil, and may return ""
// for any path it has no opinion on, to accept the default (the import
// path's last segment).
func (g GoType) Render(qualifier func(importPath string) string) string {
	switch g.Shape {
	case GoNamed:
		if g.Pkg == "" {
			return g.Name
		}
		if qualifier != nil {
			if alias := qualifier(g.Pkg); alias != "" {
				return alias + "." + g.Name
			}
		}
		return lastSegment(g.Pkg) + "." + g.Name
	case GoSlice:
		return "[]" + g.Elem.Render(qualifier)
	case GoPointer:
		return "*" + g.Elem.Render(qualifier)
	case GoMap:
		return "map[" + g.Key.Render(qualifier) + "]" + g.Elem.Render(qualifier)
	}
	return ""
}

func lastSegment(importPath string) string {
	if i := strings.LastIndexByte(importPath, '/'); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}

// scalar is one row of the projection table.
type scalar struct {
	// nomi is the Nomi type name this row projects.
	nomi string
	// goIdents are the predeclared Go type names that project TO nomi.
	// Several Go spellings may share one Nomi type — which is exactly why
	// the reverse direction is lossy and checking runs forward.
	goIdents []string
	// goStdlib is the qualified stdlib Go spelling that projects to nomi,
	// as "importPath.Name".
	goStdlib string
	// expect is the Go type nomi projects to. The zero GoType means the
	// projection is NOT unique in this direction; the row still exists so
	// a caller learns "deliberately unprovable" rather than "unrecognised
	// name".
	expect GoType
	// container and elemNomi express a projection that is a container over
	// another row — Bytes is a slice of Byte — so the element's Go
	// spelling is stated once, on the row that owns it.
	container GoShape
	elemNomi  string
	// underlying names the Nomi type this one is an opaque distinct over.
	// The boundary unwraps a distinct to its inner before dispatching (see
	// runtime.unmarshalIntoWithEnv), so whatever realizes the inner
	// realizes this too — which is what lets a Duration cross as int64
	// alongside its canonical time.Duration.
	underlying string
}

// intSpellings are the Go integer type names that project to Nomi Int.
// uint8 is absent deliberately: it is Byte, which is its own Nomi type, so
// an Int does not fill a uint8 slot and a Byte does not fill an int64 one.
var intSpellings = []string{
	"int", "int8", "int16", "int32", "int64",
	"uint", "uint16", "uint32", "uint64",
}

func named(nomi, spelling string) GoType {
	return GoType{Shape: GoNamed, Nomi: nomi, Name: spelling}
}

func stdlib(nomi, pkg, name string) GoType {
	return GoType{Shape: GoNamed, Nomi: nomi, Pkg: pkg, Name: name}
}

// table is THE projection table. Every row is one clause of the spec
// paragraph; every consumer is an index or an adapter over it.
var table = []scalar{
	{
		nomi:     NomiString,
		goIdents: []string{"string"},
		expect:   named(NomiString, "string"),
	},
	{
		nomi:     NomiBool,
		goIdents: []string{"bool"},
		expect:   named(NomiBool, "bool"),
	},
	{
		// `byte` and `uint8` are one Go type under two spellings; the
		// go/ast side sees whichever the author wrote.
		nomi:     NomiByte,
		goIdents: []string{"byte", "uint8"},
		expect:   named(NomiByte, "uint8"),
	},
	{
		nomi:     NomiInt,
		goIdents: intSpellings,
		expect:   named(NomiInt, "int64"),
	},
	{
		nomi:     NomiFloat,
		goIdents: []string{"float32", "float64"},
		expect:   named(NomiFloat, "float64"),
	},
	{
		// Bytes has no Go ident of its own: the go/ast side reaches it
		// through a slice whose element projects to Byte, which is
		// container knowledge and stays there.
		nomi:      NomiBytes,
		container: GoSlice,
		elemNomi:  NomiByte,
	},
	{
		nomi:       NomiDuration,
		goStdlib:   "time.Duration",
		expect:     stdlib(NomiDuration, "time", "Duration"),
		underlying: NomiInt,
	},
	{
		nomi:       NomiInstant,
		goStdlib:   "time.Time",
		expect:     stdlib(NomiInstant, "time", "Time"),
		underlying: NomiInt,
	},
	{
		// Dynamic is the declaration explicitly declining to constrain the
		// slot: any Go type can carry it, so nothing is provable forward.
		// The reverse direction still has an answer, which is why the row
		// carries an ident but no expectation.
		nomi:     NomiDynamic,
		goIdents: []string{"any"},
	},
	{
		// Unit carries no value, so in an element position there is no Go
		// type to compare against at all. How many Go results a declared
		// Unit return allows is a SHAPE rule, owned by the caller's arity
		// check, not by this table.
		nomi: NomiUnit,
	},
}

var (
	byNomi     = make(map[string]scalar, len(table))
	byGoIdent  = make(map[string]string, len(table)*3)
	byGoStdlib = make(map[string]string, 2)
)

func init() {
	for _, row := range table {
		byNomi[row.nomi] = row
		for _, ident := range row.goIdents {
			byGoIdent[ident] = row.nomi
		}
		if row.goStdlib != "" {
			byGoStdlib[row.goStdlib] = row.nomi
		}
	}
}

// rowExpect is the projection of one row, built for the container rows
// from the row their element lives on.
func rowExpect(row scalar) GoType {
	if row.container == GoNone {
		return row.expect
	}
	elem := rowExpect(byNomi[row.elemNomi])
	return GoType{Shape: row.container, Nomi: row.nomi, Elem: &elem}
}

// NomiForGoIdent maps a Go predeclared type name onto the Nomi type it
// projects to. This is the direction a go/ast preflight walks.
func NomiForGoIdent(name string) (string, bool) {
	nomi, ok := byGoIdent[name]
	return nomi, ok
}

// NomiForGoStdlibType maps a qualified stdlib Go type onto the Nomi type it
// projects to — `time.Duration` -> Duration, `time.Time` -> Instant.
// importPath is the RESOLVED path of the selector's package, so a local
// type that merely spells `time.Time` cannot masquerade as one.
func NomiForGoStdlibType(importPath, name string) (string, bool) {
	nomi, ok := byGoStdlib[importPath+"."+name]
	return nomi, ok
}

// Expect returns the Go type a declared Nomi type projects to. The second
// result is false when the declaration has no unique Go expectation, which
// every checking caller must treat as "say nothing": Dynamic, opaque
// distinct types, structs, type parameters, registered host handles,
// callbacks, tuples, and every name the table does not carry.
//
// A container is projected only when every element position is, because
// `[]T` with an unprovable T names no Go type — and reporting `[]something`
// would invite a caller to reject a legal binding.
func Expect(t ast.TypeExpr) (GoType, bool) {
	switch tt := t.(type) {
	case *ast.QualifiedType:
		// `strings.String` and a destructured `String` are one type; the
		// qualifier is import bookkeeping. Matching on the member name
		// accepts a same-named type from an unrelated module, which is the
		// pre-existing base-name ambiguity the rest of this boundary
		// already lives with (see runtime.isStringType).
		return Expect(tt.Member)
	case *ast.SimpleType:
		want := rowExpect(byNomi[tt.Name])
		return want, want.Shape != GoNone
	case *ast.GenericType:
		switch {
		case tt.Name == nomiList && len(tt.Params) == 1:
			return container(GoSlice, tt, tt.Params[0], nil)
		case tt.Name == nomiMaybe && len(tt.Params) == 1:
			return container(GoPointer, tt, tt.Params[0], nil)
		case tt.Name == nomiMap && len(tt.Params) == 2:
			return container(GoMap, tt, tt.Params[1], tt.Params[0])
		}
	}
	return GoType{}, false
}

// container projects one of the generic Nomi containers. key is nil for the
// single-parameter shapes.
func container(shape GoShape, self ast.TypeExpr, elem, key ast.TypeExpr) (GoType, bool) {
	elemWant, ok := Expect(elem)
	if !ok {
		return GoType{}, false
	}
	want := GoType{Shape: shape, Nomi: self.TypeString(), Elem: &elemWant}
	if key != nil {
		keyWant, ok := Expect(key)
		if !ok {
			return GoType{}, false
		}
		want.Key = &keyWant
	}
	return want, true
}

// GoFieldName is the Go struct field a Nomi struct field pairs with.
//
// It belongs beside the TYPE projection because it is the same kind of fact:
// one clause of "Go binding signatures project Nomi", applied to a field name
// rather than to a type. A Nomi struct field is snake_case and unexported
// spellings cannot cross a package boundary, so the pairing is
// snake_case -> PascalCase and it has to be IDENTICAL in every consumer — the
// generated FFI wrapper writes the field, and two derivations would pair
// `raw_query` with two different Go identifiers.
//
// It was `goStructFieldName` in internal/ffirun, reachable from one consumer
// only. Moved rather than copied: the decision gate's stated trigger is one
// fact in three or more places, and this is what put this package here.
//
// The "Field" fallback is for a name with no letters at all. It cannot arise
// from Nomi source — the lexer requires an identifier — so it is a total
// function's answer for an impossible input rather than a case anything
// reaches.
func GoFieldName(name string) string {
	var b strings.Builder
	upperNext := true
	for _, r := range name {
		if r == '_' || r == '-' {
			upperNext = true
			continue
		}
		if upperNext && r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		b.WriteRune(r)
		upperNext = false
	}
	if b.Len() == 0 {
		return "Field"
	}
	return b.String()
}
