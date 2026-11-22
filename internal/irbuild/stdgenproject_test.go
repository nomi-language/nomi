package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// TestGenericStdTypesProjectTheSameWithAndWithoutAGen holds the two readings
// of a generic std struct or host type at package-neutral arguments
// (`Range<Int>`, `Vector<String>`) to one kind: projectShared, which a std
// signature's position is, and a gen's project over the declaring module,
// which a user's annotation is. If they differed, the type would be
// admissible in a user's annotation and refused in std's own signature, or
// name two unassignable representations. The population is stdGenStructSpecs
// and stdGenHostSpecs, so an appended row is covered without editing this.
func TestGenericStdTypesProjectTheSameWithAndWithoutAGen(t *testing.T) {
	lib := std.Load()
	type row struct {
		origin, nomi string
		params       int
	}
	var rows []row
	for _, s := range stdGenStructSpecs {
		rows = append(rows, row{s.origin, s.nomi, len(s.params)})
	}
	for _, s := range stdGenHostSpecs {
		rows = append(rows, row{s.origin, s.nomi, len(s.params)})
	}
	checked := 0
	for _, r := range rows {
		module := strings.TrimPrefix(r.origin, "std/")
		fa := lib.Files[module]
		if fa == nil {
			t.Fatalf("%s: std declares no module %q", r.nomi, module)
		}
		// Distinct arguments per position, so a positional mix-up between
		// the two readings would show.
		neutral := []string{"Int", "String", "Bool", "Float"}
		args := make([]string, r.params)
		for j := range args {
			args[j] = neutral[j%len(neutral)]
		}
		src := r.nomi + "<" + strings.Join(args, ", ") + ">"
		// A name the prelude binds is not imported again, which is a
		// front-end error.
		item := strings.ReplaceAll(r.origin, "/", "::") + "." + r.nomi
		if lib.Primitives.Lookup(r.nomi) != nil {
			item = ""
		}
		_, ty := parseStdTypeImporting(t, item, src)
		if ty == nil {
			t.Errorf("%s: the checker recorded no type", src)
			continue
		}
		shared := projectShared(ty)
		if shared == kindInvalid {
			t.Errorf("%s has no shared kind, so a std signature naming it refuses as "+
				"`stdlib function outside the scalar subset`", src)
			continue
		}
		g := &gen{fa: fa}
		if inGen := g.project(ty); inGen != shared {
			t.Errorf("%s: a gen over %s projects %s and projectShared %s; they must be the same "+
				"interned def", src, module, sigKindName(inGen), sigKindName(shared))
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no generic std type row, so this comparison checked nothing")
	}
}

// TestStdRangeConstructorsLower pins `Range.naturals() -> Range<Int>`
// lowering as a shared std function. (`Range.from<T>(n: T)` is a generic
// template; a call lowers to its range at the call, rangeFrom, and a
// function value to an instance, whose body is the same literal.)
//
// Its result projects to the `Range` row's shared instance, and its body
// builds `Range{start: 0, end: None, inclusive: False}`. That literal lowers
// by the rule for every anchored generic std struct: a struct LITERAL of a
// std-anchored generic struct is built AT THE ANCHOR (stdGenStructMake), and
// the declaring module is exempt from typeOf's shadowing carve-out, since a
// module is not shadowing itself. Without that, `Range<Int>` would mean
// `rt.Range` from an annotation and a freshly minted instance from a
// literal, and the constructor would be refused for disagreeing with itself.
func TestStdRangeConstructorsLower(t *testing.T) {
	idx := stdlibLowering()
	for _, c := range []struct{ key string }{
		{"ranges.Range.naturals"},
	} {
		f := idx.byKey[c.key]
		if f == nil {
			t.Errorf("%s: absent from the index; std renamed or moved it", c.key)
			continue
		}
		if !f.lowerable() {
			t.Errorf("%s is refused as %q. The likely cause is the "+
				"anchored-literal path: stdGenStructLitAnchor stopped claiming the "+
				"`Range{...}` literal the checker typed as std/ranges' Range, in which "+
				"case `Range<Int>` has two representations. See irstruct.go.", c.key, f.why)
			continue
		}
	}
}

func sigKindName(k kind) string {
	if k == kindInvalid {
		return "<none>"
	}
	return k.nomi()
}
