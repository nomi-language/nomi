package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// TestProjectionChecksNominalIdentity is the judgement half of inferred.go,
// tested directly because every path that would exercise it through a whole
// program is refused for an independent reason: a foreign type is refused at
// the callee's own signature (`non-scalar parameter type`), so a program cannot
// reach a projection of one. The rule is defensive, and a defensive rule with
// no test reads as dead weight.
//
// It defends type identity against a match by name. A projection that
// accepted `Point` from any module would use this module's layout for another
// module's value, a wrong field offset with nothing failing. The rule is the
// analyzer's own identity, the pair `(Origin, Name)`.
func TestProjectionChecksNominalIdentity(t *testing.T) {
	point := &typeDef{nomi: "Point", lowerable: true}
	shape := &typeDef{nomi: "Shape", isEnum: true, lowerable: true}
	meters := &typeDef{nomi: "Meters", isDistinct: true, lowerable: true}
	broken := &typeDef{nomi: "Broken"} // refused: lowerable is false
	g := &gen{
		fa:    &analysis.FileAnalysis{Origin: analysis.OriginEntry},
		types: map[string]*typeDef{"Point": point, "Shape": shape, "Meters": meters, "Broken": broken},
	}

	cases := []struct {
		name string
		in   analysis.Type
		want kind
	}{
		{"this module's struct", &analysis.StructType{Origin: analysis.OriginEntry, Name: "Point"}, named(point)},
		{"this module's enum", &analysis.EnumType{Origin: analysis.OriginEntry, Name: "Shape"}, named(shape)},
		{"this module's distinct", &analysis.DistinctType{Origin: analysis.OriginEntry, Name: "Meters"}, named(meters)},
		// The whole point: same NAME, different declaring module. One pointer
		// per declaration is what makes kind equality an identity check, and
		// resolving this to `point` would hand another module's value to this
		// module's Go type.
		{"another module's same-named struct", &analysis.StructType{Origin: "shapes", Name: "Point"}, kindInvalid},
		{"a type this module does not declare", &analysis.StructType{Origin: analysis.OriginEntry, Name: "Absent"}, kindInvalid},
		// The declaration SHAPE has to agree too. If `(Origin, Name)` ever
		// stops being a real identity upstream, this is where it surfaces as a
		// refusal instead of as a body built against the wrong layout.
		{"struct name resolved as an enum", &analysis.EnumType{Origin: analysis.OriginEntry, Name: "Point"}, kindInvalid},
		{"enum name resolved as a struct", &analysis.StructType{Origin: analysis.OriginEntry, Name: "Shape"}, kindInvalid},
		// A declaration the builder already refused cannot be a kind: the
		// kind would name a type def that was never built.
		{"a refused declaration", &analysis.StructType{Origin: analysis.OriginEntry, Name: "Broken"}, kindInvalid},
		// Bool is the one type spelled as a nominal identity rather than
		// resolved through the module's own table, because it is declared in
		// std/bool and lowers to a Go `bool`. See native.go's kind comment.
		{"Bool", &analysis.EnumType{Origin: boolOrigin, Name: boolName}, kindBool},
		// And Bool's special case is keyed on the pair, not on the name: an
		// entry-declared enum called `Bool` is the module's own type.
		{"a locally declared enum named Bool", &analysis.EnumType{Origin: analysis.OriginEntry, Name: "Bool"}, kindInvalid},
		{"a generic instantiation", &analysis.StructType{
			Origin: analysis.OriginEntry, Name: "Point",
			TypeArgs: []analysis.Type{analysis.TypeInt},
		}, kindInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := g.project(tc.in); got != tc.want {
				t.Fatalf("project(%s) = %s, want %s", tc.in.String(), got.nomi(), tc.want.nomi())
			}
		})
	}
}

// TestProjectionAgreesWithTheAnnotationPath is the property that keeps `kind`
// one model instead of two.
//
// An inferred type and a written annotation naming the same Nomi type must
// produce the SAME kind — identical `==`, which for a structural type means the
// same interned pointer. If they diverged, a lambda whose parameter type was
// inferred could not be passed where one whose type was written could, and the
// symptom would be an `argument type mismatch` refusal blaming the call site
// for a projection bug.
//
// Interning makes this stronger than it looks: `intern` PANICS when one Go type
// is claimed by two Nomi spellings, so a projection that rendered
// `List<Int>`'s Nomi name differently from typeOf's would abort the build
// rather than quietly produce a second kind.
func TestProjectionAgreesWithTheAnnotationPath(t *testing.T) {
	src := `struct Point {
  x: Int
  y: Int
}

type Meters Int

enum Shape {
  Dot
  Square Int
}

fn scalars(a: Int, _b: Float, _c: String, _d: Bool, _e: Unit): Int {
  a
}

fn composites(_xs: List<Int>, _p: (Int, String), _f: (Int) -> String, _pt: Point, _m: Meters, _s: Shape): Int {
  0
}
`
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	g := &gen{nomiPath: "main.nomi", fa: p.Modules[0].FA, funcs: map[string]*fnSig{}, types: map[string]*typeDef{}, ifaces: map[string]*ifaceDef{}}
	g.pushScope()
	g.buildTypes(p.Modules[0].Nodes)

	// The annotation path and the projection path, run over the same
	// declarations: for each parameter, typeOf reads the written annotation and
	// project reads the type the analyzer recorded for that same parameter.
	checked := 0
	for _, n := range p.Modules[0].Nodes {
		fd, isFunc := n.(*ast.FuncDef)
		if !isFunc {
			continue
		}
		for _, prm := range fd.Params {
			sym, found := g.fa.Definitions[analysis.Pos{Line: prm.Line, Col: prm.Col}]
			if !found || sym.Type == nil {
				t.Fatalf("%s: parameter %s has no recorded type", fd.Name, prm.Name)
			}
			want := g.typeOf(prm.TypeAnnotation)
			if want == kindInvalid {
				t.Fatalf("%s: annotation %s did not resolve", fd.Name, prm.Name)
			}
			if got := g.project(sym.Type); got != want {
				t.Fatalf("%s: parameter %s projects to %s but its annotation reads %s",
					fd.Name, prm.Name, got.nomi(), want.nomi())
			}
			checked++
		}
	}
	if checked != 11 {
		t.Fatalf("expected to check 11 parameters, checked %d", checked)
	}
}

// TestProjectionRefusesWhatTheEmitterCannotRepresent is the subtractive rule at
// the level of the projection itself: the arms that exist mirror
// representations types.go, collections.go and lambda.go already build, and
// everything else refuses.
//
// The scalars are named ONE AT A TIME in the projection, and this is the test
// that says why: Decimal, Byte, Bytes, Any and Infallible are all
// *PrimitiveType exactly as Int is, so an arm that answered for the type rather
// than for the specific singleton would answer Int's kind for five types that
// are not Int.
func TestProjectionRefusesWhatTheEmitterCannotRepresent(t *testing.T) {
	g := &gen{fa: &analysis.FileAnalysis{Origin: analysis.OriginEntry}, types: map[string]*typeDef{}}
	cases := []struct {
		name string
		in   analysis.Type
	}{
		// The composite rows use `Infallible` as their unrepresentable part.
		// `Infallible` is uninhabited, so a kind for it would describe values
		// that cannot exist; `Any` is a top type, and a representation for it
		// is imaginable. The scalars with rt types (Decimal, Byte, Bytes) are
		// asserted positively at the end of this function.
		{"Any", analysis.TypeAny},
		{"Infallible", analysis.TypeInfallible},
		{"Map of Infallible", &analysis.MapType{Key: analysis.TypeString, Val: analysis.TypeInfallible}},
		// A record over an unrepresentable field. A record itself projects
		// (anonstruct.go); what must refuse is a record whose field has no
		// kind, which is the same rule the List and tuple rows below carry.
		{"anonymous struct of Infallible", &analysis.AnonStructType{
			Fields: []analysis.FieldDef{{Name: "x", Type: analysis.TypeInfallible}},
		}},
		{"empty anonymous struct", &analysis.AnonStructType{}},
		{"type parameter", &analysis.TypeParam_{Name_: "T"}},
		{"interface existential", &analysis.InterfaceType{Name: "Display"}},
		{"unsolved inference variable", &analysis.TypeVar{ID: 1}},
		// A list, map or tuple is only as projectable as its parts. See
		// inferred.go's MapType arm and TestProjectionAgreesWithTheAnnotationPath.
		{"List of Map of Infallible", &analysis.ListType{Elem: &analysis.MapType{Key: analysis.TypeString, Val: analysis.TypeInfallible}}},
		{"tuple with an Infallible", &analysis.TupleType{Elems: []analysis.Type{analysis.TypeInt, analysis.TypeInfallible}}},
		// A function VALUE carrying defaults is not a plain Go func: the
		// call-site arity rule lives nowhere in a kind, so flattening it
		// produces a kind that compiles and then loses an argument.
		{"func with a default", &analysis.FuncType{
			Params: []analysis.Type{analysis.TypeInt}, Return: analysis.TypeInt, DefaultCount: 1,
		}},
		{"func with a where bound", &analysis.FuncType{
			Params: []analysis.Type{analysis.TypeInt}, Return: analysis.TypeInt,
			WhereBounds: []analysis.WhereBound{{Param: &analysis.TypeParam_{Name_: "T"}}},
		}},
		{"func with no return", &analysis.FuncType{Params: []analysis.Type{analysis.TypeInt}}},
		{"nothing at all", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := g.project(tc.in); got != kindInvalid {
				t.Fatalf("%s projected to %s; it has no representation here", tc.name, got.nomi())
			}
		})
	}

	// A SOLVED inference variable is its solution, which is the one arm above
	// that must not refuse: the checker hands back a TypeVar wherever
	// unification did the work, and refusing every one of them would refuse
	// most inferred parameters in the corpus.
	if got := g.project(&analysis.TypeVar{ID: 2, Resolved: analysis.TypeInt}); got != kindInt {
		t.Fatalf("a solved TypeVar must project to its solution, got %s", got.nomi())
	}

	// A Map of representable parts projects, so an annotated and an inferred
	// map parameter agree. Asserted positively beside the subtractive rows
	// above, because a rule that only ever refuses would pass with the arm
	// deleted.
	if got := g.project(&analysis.MapType{Key: analysis.TypeString, Val: analysis.TypeInt}); got == kindInvalid {
		t.Fatal("Map<String, Int> must project: the annotated spelling lowers, so the inferred one must too")
	}

	// `Byte` and `Bytes` project, and to three different kinds counting
	// `String`: the same property in the affirmative.
	//
	// rt.Bytes is a Go `string` underneath. So a lookup keyed on the UNDERLYING
	// Go kind, or on the singleton's printed name, would hand `Bytes` and
	// `String` one kind and the builder would bind `String.trim` to a `Bytes`
	// argument without a murmur. Distinctness is the assertion; that any of
	// them projects at all is the easy part.
	byteK := g.project(analysis.TypeByte)
	bytesK := g.project(analysis.TypeBytes)
	stringK := g.project(analysis.TypeString)
	if byteK == kindInvalid || bytesK == kindInvalid {
		t.Fatalf("Byte projected to %s and Bytes to %s; both have rt types — see stdhost.go",
			byteK.nomi(), bytesK.nomi())
	}
	if byteK == bytesK || byteK == stringK || bytesK == stringK {
		t.Fatalf("Byte=%s Bytes=%s String=%s — two blessed primitives collapsed onto one kind, "+
			"which is the arm-answers-for-the-TYPE defect this test exists for",
			byteK.nomi(), bytesK.nomi(), stringK.nomi())
	}

	// `Decimal` projects, the same affirmative guard for the same reason. It
	// matters more here than for Byte/Bytes: `rt.Decimal` is a mantissa and a scale, so a lookup
	// that fell back to the underlying representation could hand it `Int`'s
	// kind, and the builder would then compare `1.50d` structurally and answer
	// `1.50d != 1.5d` — a WRONG ANSWER, not a refusal. See
	// native.go's isDecimalKind arm, which exists for exactly that.
	decK := g.project(analysis.TypeDecimal)
	if decK == kindInvalid {
		t.Fatalf("Decimal projected to %s; it has an rt type — see stdhost.go and decimal.go", decK.nomi())
	}
	if decK == kindInt || decK == kindFloat || decK == stringK {
		t.Fatalf("Decimal=%s collapsed onto Int, Float or String — the arm answered for the "+
			"underlying representation rather than for the singleton, which is the defect "+
			"this test exists for and which costs a wrong ANSWER rather than a compile error",
			decK.nomi())
	}
}
