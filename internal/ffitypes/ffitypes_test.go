package ffitypes

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

func simple(name string) ast.TypeExpr { return &ast.SimpleType{Name: name} }

func generic(name string, params ...ast.TypeExpr) ast.TypeExpr {
	return &ast.GenericType{Name: name, Params: params}
}

// TestExpect_CanonicalGoSpellings pins the forward projection — the clause
// of the spec each row encodes, rendered as the Go source a generator would
// emit and a diagnostic would name. A change here is a change to the
// boundary's contract, not to an implementation detail.
func TestExpect_CanonicalGoSpellings(t *testing.T) {
	cases := []struct {
		nomi ast.TypeExpr
		want string
	}{
		{simple(NomiString), "string"},
		{simple(NomiBool), "bool"},
		{simple(NomiByte), "uint8"},
		{simple(NomiInt), "int64"},
		{simple(NomiFloat), "float64"},
		{simple(NomiBytes), "[]uint8"},
		{simple(NomiDuration), "time.Duration"},
		{simple(NomiInstant), "time.Time"},
		{generic(nomiList, simple(NomiString)), "[]string"},
		{generic(nomiMaybe, simple(NomiInt)), "*int64"},
		{generic(nomiMap, simple(NomiString), simple(NomiInt)), "map[string]int64"},
		{generic(nomiList, generic(nomiMaybe, simple(NomiFloat))), "[]*float64"},
		// The qualified spelling of a type is the same type; the qualifier
		// is import bookkeeping.
		{&ast.QualifiedType{Module: "strings", Member: simple(NomiString)}, "string"},
	}
	for _, tc := range cases {
		got, ok := Expect(tc.nomi)
		if !ok {
			t.Errorf("Expect(%s): no expectation, want %s", tc.nomi.TypeString(), tc.want)
			continue
		}
		if got.String() != tc.want {
			t.Errorf("Expect(%s) = %s, want %s", tc.nomi.TypeString(), got, tc.want)
		}
	}
}

// TestExpect_NoUniqueExpectation is the conservatism of the table stated
// directly: these declarations have more than one legal Go spelling (or
// none), so the table must decline to name one. Every caller keys "skip" off
// this, so a row that started answering here would turn into false
// rejections at three call sites at once.
func TestExpect_NoUniqueExpectation(t *testing.T) {
	cases := []struct {
		name string
		nomi ast.TypeExpr
	}{
		{"Dynamic declines to constrain the slot", simple(NomiDynamic)},
		{"Unit carries no value", simple(NomiUnit)},
		{"an opaque distinct type has several legal spellings", simple("Meters")},
		{"a struct name projects to a shape, not a type", simple("Point")},
		{"a type parameter stands for whatever the call site supplies", simple("T")},
		{"a container over an unprovable element is unprovable", generic(nomiList, simple("Meters"))},
		{"a map with an unprovable key is unprovable", generic(nomiMap, simple("Meters"), simple(NomiInt))},
		{"a callback is not an element type this table projects", &ast.FuncType{Params: []ast.TypeExpr{simple(NomiInt)}, Return: simple(NomiInt)}},
		{"a tuple is not an element type this table projects", &ast.FuncType{Params: []ast.TypeExpr{simple(NomiInt), simple(NomiInt)}}},
		{"an arity the container rule does not define", generic(nomiMap, simple(NomiString))},
		{"nil", nil},
	}
	for _, tc := range cases {
		got, ok := Expect(tc.nomi)
		if ok {
			t.Errorf("%s: Expect returned %s, want no expectation", tc.name, got)
		}
	}
}

// TestNomiForGoSpellings covers the reverse direction the go/ast preflight
// walks: several Go spellings share one Nomi type, which is why that
// direction cannot be inverted to a check.
func TestNomiForGoSpellings(t *testing.T) {
	idents := map[string]string{
		"string":  NomiString,
		"bool":    NomiBool,
		"byte":    NomiByte,
		"uint8":   NomiByte,
		"int":     NomiInt,
		"int32":   NomiInt,
		"int64":   NomiInt,
		"uint64":  NomiInt,
		"float32": NomiFloat,
		"float64": NomiFloat,
		"any":     NomiDynamic,
	}
	for ident, want := range idents {
		got, ok := NomiForGoIdent(ident)
		if !ok || got != want {
			t.Errorf("NomiForGoIdent(%q) = %q, %v; want %q, true", ident, got, ok, want)
		}
	}
	for _, ident := range []string{"error", "uintptr", "complex128", "Duration", ""} {
		if got, ok := NomiForGoIdent(ident); ok {
			t.Errorf("NomiForGoIdent(%q) = %q, want no projection", ident, got)
		}
	}
	if got, ok := NomiForGoStdlibType("time", "Duration"); !ok || got != NomiDuration {
		t.Errorf("NomiForGoStdlibType(time.Duration) = %q, %v", got, ok)
	}
	if got, ok := NomiForGoStdlibType("time", "Time"); !ok || got != NomiInstant {
		t.Errorf("NomiForGoStdlibType(time.Time) = %q, %v", got, ok)
	}
	// A local type that merely spells `Time` is not time.Time.
	if got, ok := NomiForGoStdlibType("example.com/app", "Time"); ok {
		t.Errorf("NomiForGoStdlibType(app.Time) = %q, want no projection", got)
	}
}
