package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestStdSpecSpellingsAreTheirKinds holds each std spec row's stated kinds to
// the types std's declarations write.
//
// A row's validation compares spellings only (typeText), so it resolves no
// type and needs no other row admitted first. What the spelling stands for is
// checked here instead: the checker's type for each annotation std writes,
// projected with no gen (projectShared), must be the kind the row builds its
// def with. A row whose spelling matches std while its kind says something
// else (`zone: String` stored as an Int) fails here, and nowhere else.
func TestStdSpecSpellingsAreTheirKinds(t *testing.T) {
	lib := std.Load()
	checked := 0
	sameType := func(where string, te ast.TypeExpr, got analysis.Type, want kind) {
		t.Helper()
		checked++
		if got == nil {
			t.Errorf("%s: the checker recorded no type for `%s`", where, typeText(te))
			return
		}
		if k := projectShared(got); k != want {
			t.Errorf("%s: std writes `%s`, which projects as %s; the row's kind is %s",
				where, typeText(te), k.nomi(), want.nomi())
		}
	}
	same := func(where string, fa *analysis.FileAnalysis, te ast.TypeExpr, want kind) {
		t.Helper()
		sameType(where, te, fa.TypeExprTypes[te], want)
	}

	defs := stdStructDefs()
	for i := range stdStructSpecs {
		s := &stdStructSpecs[i]
		fa := lib.Files[strings.TrimPrefix(s.origin, "std/")]
		decl := stdStructDeclIn(fa, s)
		if decl == nil || len(decl.Fields) != len(s.fields) {
			t.Errorf("%s.%s: no declaration of the row's shape (TestStdStructSpecsMatchStdSource says why)", s.origin, s.nomi)
			continue
		}
		for j, f := range decl.Fields {
			same(s.origin+"."+s.nomi+"."+f.Name, fa, f.TypeAnnotation, s.fields[j].kindOf(defs))
		}
	}

	for i := range opaqueSpecs {
		s := &opaqueSpecs[i]
		fa := lib.Files[strings.TrimPrefix(s.origin, "std/")]
		decl := opaqueDeclIn(fa, s)
		if decl == nil {
			t.Errorf("%s.%s: no declaration of the row's shape", s.origin, s.nomi)
			continue
		}
		same(s.origin+"."+s.nomi, fa, decl.InnerTypeExpr, s.inner)
	}

	enumDefs := stdEnumDefs()
	for i := range stdEnumSpecs {
		s := &stdEnumSpecs[i]
		fa := lib.Files[strings.TrimPrefix(s.origin, "std/")]
		decl := stdEnumDeclIn(fa, s)
		if decl == nil {
			t.Errorf("%s.%s: no declaration of the row's shape", s.origin, s.nomi)
			continue
		}
		for j := range s.variants {
			v, got := &s.variants[j], decl.Variants[j]
			where := s.origin + "." + s.nomi + "." + v.nomi
			switch v.form {
			case payloadBare:
			case payloadStructScalar:
				same(where, fa, got.Fields[0].TypeAnnotation, v.scalar)
			case payloadStructFields:
				for k := range v.fields {
					same(where+"."+v.fields[k].nomi, fa, got.Fields[k].TypeAnnotation, v.fields[k].fieldKind())
				}
			default:
				same(where, fa, got.DataTypeExpr, v.payloadKind(enumDefs))
			}
		}
	}

	for i := range stdIfaceSpecs {
		s := &stdIfaceSpecs[i]
		fa := lib.Files[strings.TrimPrefix(s.module, "std/")]
		decl, _ := resolveSymbol(fa.ModuleScope.Lookup(s.nomi)).Node.(*ast.InterfaceDef)
		if decl == nil || !s.matches(decl) {
			t.Errorf("%s.%s: no declaration of the row's shape", s.module, s.nomi)
			continue
		}
		var required []*ast.InterfaceMethod
		for j := range decl.Methods {
			if !stdIfaceNomiDefault(&decl.Methods[j]) {
				required = append(required, &decl.Methods[j])
			}
		}
		for j := range s.methods {
			ms, im := &s.methods[j], required[j]
			where := s.module + "." + s.nomi + "." + ms.name
			for k, p := range ms.params {
				if !p.self {
					same(where+"("+p.name+")", fa, im.Params[k].TypeAnnotation, p.kind())
				}
			}
			if ms.result != "" {
				same(where+" result", fa, im.ReturnTypeExpr, ms.resultKind())
			}
		}
	}

	for i := range preludeSpecs {
		s := &preludeSpecs[i]
		a := preludeDeclaringAnchor(s)
		if a == nil {
			t.Errorf("%s.%s: no declaration of the row's shape", s.origin, s.nomi)
			continue
		}
		// A generic enum's payload annotations are not in TypeExprTypes; the
		// declared type's variants carry them.
		fa := lib.Files[strings.TrimPrefix(s.origin, "std/")]
		et, _ := resolveSymbol(fa.ModuleScope.Lookup(s.nomi)).Type.(*analysis.EnumType)
		for j, v := range s.variants {
			if v.param < 0 && v.namedPayload == nil && v.carries() {
				var declared analysis.Type
				if et != nil {
					declared = et.Variants[j].DataType
				}
				sameType(s.origin+"."+s.nomi+"."+v.nomi, a.decl.Variants[j].DataTypeExpr, declared, v.fixed)
			}
		}
	}

	for _, s := range namedPayloadSpecs {
		fa := lib.Files[strings.TrimPrefix(s.origin, "std/")]
		decl, _ := resolveSymbol(fa.ModuleScope.Lookup(s.nomi)).Node.(*ast.EnumDef)
		if decl == nil || !s.matchesDecl(decl) {
			t.Errorf("%s.%s: no declaration of the row's shape", s.origin, s.nomi)
			continue
		}
		for j, v := range s.variants {
			if v.payload != kindInvalid {
				same(s.origin+"."+s.nomi+"."+v.nomi, fa, decl.Variants[j].DataTypeExpr, v.payload)
			}
		}
	}
	t.Logf("%d written types checked", checked)
}
