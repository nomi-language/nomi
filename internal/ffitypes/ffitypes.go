// Package ffitypes is the single encoding of the scalar Nomi <-> Go type
// projection the FFI boundary is defined by (spec: "Go binding signatures
// project Nomi ...").
//
// The table is representation-neutral: it is expressed in Nomi type names
// and Go source spellings, and nothing in this file mentions reflect or
// go/ast. internal/ffirun walks a Go signature as go/ast and asks which Nomi
// type each Go spelling projects to, so it can compare that against the text
// of the declaration (NomiForGoIdent, NomiForGoStdlibType).
//
// One table because there is one spec paragraph. A second copy would be the
// same knowledge written twice, and the copies would drift on the first spec
// change.
package ffitypes

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

// scalar is one row of the projection table.
type scalar struct {
	// nomi is the Nomi type name this row projects.
	nomi string
	// goIdents are the predeclared Go type names that project TO nomi.
	// Several Go spellings may share one Nomi type, which is why the reverse
	// direction is lossy.
	goIdents []string
	// goStdlib is the qualified stdlib Go spelling that projects to nomi,
	// as "importPath.Name".
	goStdlib string
}

// intSpellings are the Go integer type names that project to Nomi Int.
// uint8 is absent deliberately: it is Byte, which is its own Nomi type, so
// an Int does not fill a uint8 slot and a Byte does not fill an int64 one.
var intSpellings = []string{
	"int", "int8", "int16", "int32", "int64",
	"uint", "uint16", "uint32", "uint64",
}

// table is THE projection table. Every row is one clause of the spec
// paragraph; every consumer is an index over it.
var table = []scalar{
	{nomi: NomiString, goIdents: []string{"string"}},
	{nomi: NomiBool, goIdents: []string{"bool"}},
	// `byte` and `uint8` are one Go type under two spellings; the go/ast
	// side sees whichever the author wrote.
	{nomi: NomiByte, goIdents: []string{"byte", "uint8"}},
	{nomi: NomiInt, goIdents: intSpellings},
	{nomi: NomiFloat, goIdents: []string{"float32", "float64"}},
	// Bytes has no Go ident of its own: the go/ast side reaches it through a
	// slice whose element projects to Byte, which is container knowledge and
	// stays there.
	{nomi: NomiBytes},
	{nomi: NomiDuration, goStdlib: "time.Duration"},
	{nomi: NomiInstant, goStdlib: "time.Time"},
	// Dynamic is the declaration explicitly declining to constrain the slot:
	// any Go type can carry it.
	{nomi: NomiDynamic, goIdents: []string{"any"}},
	// Unit carries no value. How many Go results a declared Unit return
	// allows is a SHAPE rule, owned by the caller's arity check.
	{nomi: NomiUnit},
}

var (
	byGoIdent  = make(map[string]string, len(table)*3)
	byGoStdlib = make(map[string]string, 2)
)

func init() {
	for _, row := range table {
		for _, ident := range row.goIdents {
			byGoIdent[ident] = row.nomi
		}
		if row.goStdlib != "" {
			byGoStdlib[row.goStdlib] = row.nomi
		}
	}
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
