package irbuild

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// `?` is the one identifier character Nomi admits and Go does not, and the lexer
// admits it only as a SUFFIX (`isIdentPart` deliberately excludes it
// mid-identifier). The builder's Go-spelled names route through `goLocal`,
// `goIdent`, `goFuncName`, `mintInspectorName` or `sanitizeIdent`, which map
// it to a character Go accepts.

// TestIRBuild_ThePropertyCatchesAnUnsanitizedName checks the two Go-side
// checks a sanitized name is held to: a `?` in a declared name must fail BOTH
// `parser.ParseFile` and `token.IsIdentifier`, and the sanitized spelling must
// pass both.
func TestIRBuild_ThePropertyCatchesAnUnsanitizedName(t *testing.T) {
	const bad = "package nomimod0\n\nfunc nomiTail_even?(n int64) bool { return n == 0 }\n"
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "mod.go", bad, parser.SkipObjectResolution); err == nil {
		t.Fatal("parser.ParseFile accepted a `?` in a function name, so the property above is blind")
	}
	if token.IsIdentifier("nomiTail_even?") {
		t.Fatal("token.IsIdentifier accepted `nomiTail_even?`, so naming the offender is blind")
	}
	// And the sanitized form is accepted, so the check is not simply always-no.
	good := strings.ReplaceAll(bad, "nomiTail_even?", "nomiTail_even_")
	if _, err := parser.ParseFile(fset, "mod.go", good, parser.SkipObjectResolution); err != nil {
		t.Fatalf("the sanitized spelling must parse: %v", err)
	}
	if !token.IsIdentifier("nomiTail_even_") {
		t.Fatal("token.IsIdentifier rejected the sanitized spelling")
	}
}
