package analysis_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A refinement has its base's impls, save those of an interface that makes a
// value of its type (refinement_impls.go): an explicit refinement there is an
// error, an inferred one is the base, and the interfaces whose functions
// only take a self still serve a refinement. FromJson reads a refinement at its
// base and checks it (refinement_reads.go).

const refinementImplsSource = `import
    std::json.Json
    std::json.ToJson
    std::parse
    std::parse.Parse
end

refine Port Int when fn p -> p >= 1 and p <= 65535

fn read<T>(s: String) -> Option<T> where T: Parse<String>
    case T.parse(s)
        Ok(v)  -> Some(v)
        Err(_) -> None
    end
end

fn uses(x: Int) -> Option<Json>
    fields = parse.read_fields<{v: Int}>({"v" => "1"})
    Option.map(fields, fn r -> ToJson.to_json(r.v + x))
end

fn f(p: Port, q: Port, ports: List<Port>, sizes: Range<NonNegativeInt>)
    %s
end
`

func refinementImplsCheck(body string) []analysis.TypeError {
	_, errs := checkSourceWithStdlib(strings.Replace(refinementImplsSource, "%s", body, 1))
	return withoutUnusedBindingErrors(errs)
}

func TestRefinementImpls_AnInterfaceThatMakesAValueIsNotTheBases(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"parse", `dbg Parse.parse<Port>("0")`,
			"Port has no impl of Parse (required by `Parse.parse`): Port is a refinement of Int, and Int's `Parse.parse` makes an Int that Port's rule has not checked"},
		{"generic parse", `dbg read<Port>("0")`,
			"Port has no impl of Parse<String> (required by `where T: Parse<String>`)"},
		{"empty sum", `dbg Summable.empty_sum<Port>()`,
			"Port has no impl of Summable (required by `Summable.empty_sum`)"},
		{"empty sum by its result", "z: Port = Summable.empty_sum()\n    dbg z",
			"Port has no impl of Summable (required by `Summable.empty_sum`)"},
		{"empty product", `dbg Productable.empty_product<Port>()`,
			"Port has no impl of Productable (required by `Productable.empty_product`)"},
		{"a range of a refinement", `dbg Iter.to_list(sizes)`,
			"NonNegativeInt has no impl of Discrete: NonNegativeInt is a refinement of Int, and Int's `Discrete.next` makes an Int"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := refinementImplsCheck(tc.body)
			var got []analysis.TypeError
			for _, e := range errs {
				if e.Code == analysis.RefinementImplMakesBaseCode {
					got = append(got, e)
				}
			}
			if len(got) != 1 {
				t.Fatalf("%s: want one %s error, got %d; errors: %v", tc.body, analysis.RefinementImplMakesBaseCode, len(got), errs)
			}
			if !strings.Contains(got[0].Message, tc.want) {
				t.Fatalf("%s:\n got %q\nwant %q", tc.body, got[0].Message, tc.want)
			}
			if hint := strings.Join(got[0].Hints, "\n"); !strings.Contains(hint, ".try_from(...)`, which is `Some` when it satisfies") {
				t.Fatalf("%s: the help does not name try_from: %q", tc.body, hint)
			}
		})
	}
}

// FromJson reads a refinement at its base and checks the refinement's rules, at
// the top, inside a std container and in a record's field, and a record
// read from texts reads a refinement field with its base's Parse.
func TestRefinementImpls_FromJsonReadsARefinement(t *testing.T) {
	for _, body := range []string{
		`dbg Json.decode_as<Port>("0")`,
		`dbg Json.decode_as<List<Port>>("[0]")`,
		`dbg Json.decode_as<{port: Port}>("{}")`,
		`dbg Json.decode_as<Map<String, Option<Port>>>("{}")`,
		`dbg Json.decode_as<NonEmptyList<Int>>("[]")`,
		`dbg Json.decode_as<{n: NonNegativeInt and NonZeroInt}>("{}")`,
		`dbg parse.read_fields<{v: Port}>({"v" => "0"})`,
		`dbg parse.write_fields({v: p})`,
	} {
		if errs := refinementImplsCheck(body); len(errs) > 0 {
			t.Errorf("%s: the front end refuses it: %v", body, errs)
		}
	}
}

// A generic type's impl is one for every instance of one kind, so a `where`
// of the program's own impl reads no refinement: `Box<Port>` is no FromJson,
// with the help that names reading `Box<Int>` and checking.
func TestRefinementImpls_AProgramsGenericImplReadsNoRefinement(t *testing.T) {
	src := `import std::json.{FromJson, Json}

refine Port Int when fn p -> p >= 1

struct Box<T>
    v: T
end

derive FromJson for Box

fn main()
    dbg Json.decode_as<Box<Port>>("{}")
end
`
	_, errs := checkSourceWithStdlib(src)
	errs = withoutUnusedBindingErrors(errs)
	if len(errs) != 1 {
		t.Fatalf("want the one error for Box<Port>, got %v", errs)
	}
	if want := "Box<Port> does not implement FromJson (required by `where T: FromJson`)"; !strings.Contains(errs[0].Message, want) {
		t.Fatalf("got %q, want %q", errs[0].Message, want)
	}
	if hint := strings.Join(errs[0].Hints, "\n"); !strings.Contains(hint, "read Box<Int> and check the value with `Port.try_from(...)`") {
		t.Fatalf("the help does not name the read at the base: %q", hint)
	}
	if _, errs := checkSourceWithStdlib(strings.Replace(src, "decode_as<Box<Port>>", "decode_as<Box<Int>>", 1)); len(withoutUnusedBindingErrors(errs)) > 0 {
		t.Fatalf("Box<Int> is refused too, so the refusal is not Port's: %v", errs)
	}
}

// Every function of an interface that reads a refinement whose result names
// its type has a breach a read at a refinement answers: the set of readers is
// held to what the IR builder lowers (internal/irbuild's refinementread.go).
func TestRefinementImpls_EveryReaderFunctionHasABreach(t *testing.T) {
	fa, errs := checkSourceWithStdlib("fn main()\n    dbg 1\nend\n")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	got := analysis.RefinementReaderBreachesForTest(fa)
	if len(got) == 0 {
		t.Fatal("no interface reads a refinement, so nothing below is tested")
	}
	for fn, breach := range got {
		if breach == 0 {
			t.Errorf("%s reads a refinement and answers no failure a breach of its rules can be", fn)
		}
	}
	if got["FromJson.from_json"] != analysis.BreachShapeError || got["FromJson.from_missing"] != analysis.BreachNone {
		t.Errorf("FromJson's breaches: %v", got)
	}
}

func TestRefinementImpls_AnInferredRefinementIsTheBase(t *testing.T) {
	for _, body := range []string{
		"total: Int = Iter.sum(ports)\n    dbg total",
		"total: Int = ports |> Iter.sum()\n    dbg total",
		"product: Int = Iter.product(ports)\n    dbg product",
		"next: Option<Int> = Discrete.next(p)\n    dbg next",
		"stepped: Option<Int> = Steppable.step_by(p, 2)\n    dbg stepped",
		// Comparable makes no value: its min, max and clamp are defaults
		// that answer a value they were given.
		"best: Option<Port> = Iter.max(ports)\n    dbg best",
		"sorted: List<Port> = Iter.sort(ports)\n    dbg sorted",
		"higher: Port = Comparable.max(p, q)\n    dbg higher",
		"dbg Display.to_string(p)",
		"dbg ToJson.to_json(p)",
		"dbg p == q",
		"dbg Set[p, q]",
		"dbg {p => 1}",
	} {
		if errs := refinementImplsCheck(body); len(errs) > 0 {
			t.Errorf("%s: the front end refuses it: %v", body, errs)
		}
	}
}

func TestRefinementImpls_AValueMadeAtTheBaseIsNoRefinement(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"total: Port = Iter.sum(ports)\n    dbg total", "type mismatch: expected Port, got Int"},
		{"next: Option<Port> = Discrete.next(p)\n    dbg next", "expected Option<Port>, got Option<Int>"},
	} {
		errs := refinementImplsCheck(tc.body)
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, tc.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %q, got %v", tc.body, tc.want, errs)
		}
	}
}

// TestRefinementImpls_EveryStdInterfaceIsClassified holds the std interfaces
// whose impls a refinement does not have from its base to the list the spec
// and the explanation of refinement-impl-makes-base name: each std interface a program reaches is
// listed here, as one that makes a value of its type or one that does not.
func TestRefinementImpls_EveryStdInterfaceIsClassified(t *testing.T) {
	makes := map[string]bool{
		"Parse": true, "ParseFields": true, "FromJson": true, "FromSql": true, "Summable": true,
		"Productable": true, "Discrete": true, "Steppable": true, "From": true, "TryFrom": true,
		"Variants": true, "Struct": true, "FromValue": true,
	}
	fa, errs := checkSourceWithStdlib("fn main()\n    dbg 1\nend\n")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	scopes := fa.Program().StdlibModuleScopes
	modules := make([]string, 0, len(scopes))
	for m := range scopes {
		modules = append(modules, m)
	}
	sort.Strings(modules)
	seen := map[string]bool{}
	for _, m := range modules {
		for name, sym := range scopes[m].Symbols {
			if sym.Resolved != nil {
				continue
			}
			if _, ok := sym.Type.(*analysis.InterfaceType); !ok {
				continue
			}
			got, found := analysis.InterfaceMakesSelfForTest(fa, name, "std/"+m)
			if !found {
				continue
			}
			seen[name] = true
			if got != makes[name] {
				t.Errorf("std/%s's %s: makes a value of its type = %v, want %v; list it in this test, the spec (§4 *Refinements*) and refinement-impl-makes-base's explanation", m, name, got, makes[name])
			}
		}
	}
	for name := range makes {
		if !seen[name] && name != "FromValue" {
			t.Errorf("%s is listed as making a value of its type and no std module declares it", name)
		}
	}
	for _, name := range []string{"Comparable", "Equatable", "Hashable", "Display", "ToJson", "Iter"} {
		if !seen[name] {
			t.Errorf("%s was not reached: the walk over std's interfaces is not complete", name)
		}
	}
}
