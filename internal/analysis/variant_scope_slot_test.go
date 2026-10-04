package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A same-file enum variant's bare name yields its module-scope slot to any
// other declaration or import of that name (variantMayTakeScopeSlot). The
// spec never lets a same-file variant be referenced bare, so the slot is
// worth nothing to the variant, while a type, interface or import needs it
// for type position, impl headers and owner calls.

// variantSlotSrcInterface names a variant after an imported interface and
// uses both: the interface in a bound, an impl header and an interface-
// qualified call; the variant qualified, dot-leading, and in patterns.
const variantSlotSrcInterface = `import std/json.{Json, ToJson}

enum Op {
    ToJson
    Other
}

struct P {
    n: Int
}

impl ToJson for P {
    fn to_json(p: P): Json {
        Json.Int(p.n)
    }
}

fn encode<T>(v: T): Json where T: ToJson {
    ToJson.to_json(v)
}

fn name(op: Op): String {
    case op {
        .ToJson -> "to_json"
        Op.Other -> "other"
    }
}

fn qualified(op: Op): Bool {
    case op {
        Op.ToJson -> True
        _ -> False
    }
}

pub fn main(): String {
    a: Op = .ToJson
    b = Op.ToJson
    j = encode(P{n: 1})
    name(a) + name(b) + Json.encode(j)
}
`

func TestVariantSlot_ImportedInterfaceOutranksASameNamedVariant(t *testing.T) {
	errs := reservedNamesCheckAll(t, variantSlotSrcInterface)
	if len(errs) != 0 {
		t.Fatalf("a variant named like an imported interface must not disturb either; got: %v", errs)
	}
	fa, _ := reservedNamesFA(t, variantSlotSrcInterface)
	sym := fa.ModuleScope.LookupLocal("ToJson")
	if sym == nil {
		t.Fatal("ToJson is absent from module scope")
	}
	if _, imported := sym.Node.(*ast.ImportStmt); !imported {
		t.Fatalf("module scope answers ToJson with %v (%T); want the import", sym.Kind, sym.Node)
	}
	op := fa.ModuleScope.LookupLocal("Op")
	if op == nil || op.Members["ToJson"] == nil || op.Members["ToJson"].Kind != analysis.SymbolEnumVariant {
		t.Fatal("the variant must stay reachable through its enum as Op.ToJson")
	}
}

// variantSlotSrcType names a variant after an imported type and uses the
// type in type position, an owner call and an impl header. (A user file
// cannot re-import a prelude name, so the std/json shape, a variant `String`
// beside `import strings.String`, is covered over the stdlib below.)
const variantSlotSrcType = `import std/calendar.Date

pub enum Value {
    Date Date
    Count Int
}

pub interface Shout {
    fn shout(v: self): String
}

impl Shout for Date {
    fn shout(v: Date): String {
        Display.to_string(v)
    }
}

fn span(a: Date, b: Date): Int {
    Date.days_between(a, b)
}

fn describe(v: Value): String {
    case v {
        .Date(d) -> Shout.shout(d) + Int.to_string(span(d, d))
        Value.Count(n) -> Int.to_string(n)
    }
}

pub fn main(): String {
    describe(.Count(2))
}
`

func TestVariantSlot_ImportedTypeOutranksASameNamedVariant(t *testing.T) {
	errs := reservedNamesCheckAll(t, variantSlotSrcType)
	if len(errs) != 0 {
		t.Fatalf("a variant named like an imported type must not disturb either; got: %v", errs)
	}
	fa, _ := reservedNamesFA(t, variantSlotSrcType)
	sym := fa.ModuleScope.LookupLocal("Date")
	if sym == nil {
		t.Fatal("Date is absent from module scope")
	}
	if _, imported := sym.Node.(*ast.ImportStmt); !imported {
		t.Fatalf("module scope answers Date with %v (%T); want the import", sym.Kind, sym.Node)
	}
	// The impl header names the real std/calendar Date, so its identity key
	// carries that origin, not this file's.
	idx := fa.ProjectImpls
	if idx == nil {
		t.Fatal("nil ProjectImpls")
	}
	if !idx.ImplsByIdentity[analysis.ImplIdentityKey{Origin: "std/calendar", Type: "Date", Iface: "Shout"}] {
		t.Error("impl Shout for Date is not keyed under std/calendar")
	}
	if idx.ImplsByIdentity[analysis.ImplIdentityKey{Origin: analysis.OriginEntry, Type: "Date", Iface: "Shout"}] {
		t.Error("impl Shout for Date is keyed under the entry file, i.e. under the variant")
	}
}

// TestVariantSlot_LocalTypeOutranksASameNamedVariantInEitherOrder: the same
// rule between two declarations of one file, independent of source order.
func TestVariantSlot_LocalTypeOutranksASameNamedVariantInEitherOrder(t *testing.T) {
	structDecl := "pub struct Circle {\n    r: Float\n}\n"
	enumDecl := "pub enum Shape {\n    Circle Float\n    Dot\n}\n"
	use := "\npub fn area(c: Circle): Float {\n    c.r\n}\n\npub fn make(): Shape {\n    Shape.Circle(1.0)\n}\n"
	for _, c := range []struct{ name, src string }{
		{"struct first", structDecl + "\n" + enumDecl + use},
		{"enum first", enumDecl + "\n" + structDecl + use},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, e := range reservedNamesCheckAll(t, c.src) {
				t.Errorf("unexpected error: %s", e.Message)
			}
			fa, _ := reservedNamesFA(t, c.src)
			sym := fa.ModuleScope.LookupLocal("Circle")
			if sym == nil || sym.Kind != analysis.SymbolStruct {
				t.Fatalf("module scope answers Circle with %+v; want the struct", sym)
			}
		})
	}
}

// TestVariantSlot_TwoDeclarationsStillCollide is the boundary: the rule makes
// a variant yield, and nothing else. Two non-variant declarations of one name
// are still a redeclaration.
func TestVariantSlot_TwoDeclarationsStillCollide(t *testing.T) {
	src := "pub struct Circle {\n    r: Float\n}\n\npub enum Circle {\n    A\n}\n"
	for _, e := range reservedNamesCheckAll(t, src) {
		if strings.Contains(e.Message, "'Circle' is already defined in this scope") {
			return
		}
	}
	t.Fatal("struct Circle beside enum Circle is no longer reported")
}

// TestVariantSlot_StdJsonImplsCarryTheRealTypesIdentity: std/json declares
// `enum Json { String String  Int Int  Float Float ... }` and imports
// strings.String, int.Int and float.Float. Its ToJson/FromJson impls for those
// receivers are keyed under the declaring modules, never under std/json.
func TestVariantSlot_StdJsonImplsCarryTheRealTypesIdentity(t *testing.T) {
	idx := stdlibProjectIndex(t)
	for _, c := range []struct{ typ, origin string }{
		{"String", "std/strings"},
		{"Int", "std/int"},
		{"Float", "std/float"},
	} {
		for _, iface := range []string{"ToJson", "FromJson"} {
			if !idx.ImplsByIdentity[analysis.ImplIdentityKey{Origin: c.origin, Type: c.typ, Iface: iface}] {
				t.Errorf("impl %s for %s is not keyed under %s", iface, c.typ, c.origin)
			}
			if idx.ImplsByIdentity[analysis.ImplIdentityKey{Origin: "std/json", Type: c.typ, Iface: iface}] {
				t.Errorf("impl %s for %s is keyed under std/json, the variant's file", iface, c.typ)
			}
		}
		for _, method := range []string{"to_json", "from_json"} {
			if idx.LookupTypeMethodByIdentity(c.origin, c.typ, method) == nil {
				t.Errorf("%s.%s is not reachable by %s's identity", c.typ, method, c.origin)
			}
			if idx.LookupTypeMethodByIdentity("std/json", c.typ, method) != nil {
				t.Errorf("%s.%s is keyed under std/json, the variant's file", c.typ, method)
			}
		}
	}
}
