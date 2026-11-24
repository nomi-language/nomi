package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// The functions the compiler provides for a type with a rule in its chain,
// `try_from` and `unchecked` (spec §4 *Validated types*), build a value of
// the type from something else. For an opaque type that is construction,
// private to its defining file and that file's partner test, as its call
// form is (spec §4 *Opaque distinct types*). An opaque enum has no rule and
// so no provided conversion.

// conversionsModule declares an opaque type over a refinement (Uid), an opaque
// validated type (Pct), an opaque validated struct (Span), a validated type
// that is not opaque (Open), and an opaque validated type whose file writes
// an `impl TryFrom<Celsius>` of its own beside the provided one (Kelvin).
// Its own functions call each provided conversion.
const conversionsModule = `pub opaque type Uid PositiveInt

pub opaque type Pct Int when fn n -> n >= 0 and n <= 100

pub opaque struct Span
    low: Int
    high: Int
    when fn s -> s.low <= s.high
end

pub type Open Int when fn n -> n >= 0

pub type Celsius Float when fn c -> c >= -273.15

pub opaque type Kelvin Float when fn k -> k >= 0.0

impl TryFrom<Celsius> for Kelvin
    fn try_from(Celsius(c)) -> Option<Kelvin>
        Kelvin.try_from(c + 273.15)
    end
end

pub fn uid(n: Int) -> Option<Uid>
    Uid.try_from(n)
end

pub fn pct(n: Int) -> Option<Pct>
    Pct.try_from(n)
end

pub fn clamped(n: Int) -> Pct
    Pct.unchecked(if n < 0 then 0 else if n > 100 then 100 else n)
end

pub fn span(low: Int, high: Int) -> Option<Span>
    Span.try_from({low:, high:})
end
`

// checkConversions checks the program of conversionsModule as `ids` and
// files, entered at entry, and answers every file's diagnostics.
func checkConversions(t *testing.T, entry string, files map[string]string) []analysis.TypeError {
	t.Helper()
	texts := map[string]string{"ids": conversionsModule}
	for k, v := range files {
		texts[k] = v
	}
	p := newManager().CheckProgram("/nomi-opaque-conversions", entry, texts)
	var out []analysis.TypeError
	for _, key := range p.Keys() {
		out = append(out, p.Errors[key]...)
	}
	return out
}

func diagList(errs []analysis.TypeError) string {
	var b strings.Builder
	for _, e := range errs {
		fmt.Fprintf(&b, "\n  %d:%d %s", e.Line, e.Col, e.Message)
	}
	return b.String()
}

// TestOpaqueConversion_RefusedOutsideTheDefiningFile: each provided
// conversion of an opaque type, called, named as a value, captured, or
// chosen among the type's `TryFrom` impls, is an error
// outside the type's file, at the function's name, coded as opaque
// construction.
func TestOpaqueConversion_RefusedOutsideTheDefiningFile(t *testing.T) {
	cases := []struct {
		name, body, typ, member string
		col                     int
	}{
		{"a type over a refinement", "    dbg Uid.try_from(5)\n", "Uid", "try_from", 13},
		{"a validated type", "    dbg Pct.try_from(5)\n", "Pct", "try_from", 13},
		{"a validated struct", "    dbg Span.try_from({low: 1, high: 2})\n", "Span", "try_from", 14},
		{"unchecked", "    dbg Pct.unchecked(5)\n", "Pct", "unchecked", 13},
		{"a function value", "    f = Pct.try_from\n    dbg f(5)\n", "Pct", "try_from", 13},
		{"a capture", "    f = &Pct.try_from(&1)\n    dbg f(5)\n", "Pct", "try_from", 14},
		{"chosen among the impls", "    dbg Kelvin.try_from(20.0)\n", "Kelvin", "try_from", 16},
		{"a std type", "    dbg Codepoint.try_from(65)\n", "Codepoint", "try_from", 19},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkConversions(t, "main", map[string]string{
				"main": "import ids.{Uid, Pct, Span, Kelvin}\n\nfn main()\n" + tc.body + "end\n",
			})
			want := fmt.Sprintf("`%s.%s` constructs the opaque type '%s', whose construction is private to its defining module — use an exported constructor function", tc.typ, tc.member, tc.typ)
			var found []analysis.TypeError
			for _, e := range errs {
				if e.Message == want {
					found = append(found, e)
				}
			}
			if len(found) != 1 {
				t.Fatalf("want one error %q, got:%s", want, diagList(errs))
			}
			if e := found[0]; e.Line != 4 || e.Col != tc.col || e.Code != analysis.OpaqueConstructionCode || len(e.Args) != 1 || e.Args[0] != tc.typ {
				t.Fatalf("the error is at %d:%d with code %q %v, want 4:%d with %q [%s]", e.Line, e.Col, e.Code, e.Args, tc.col, analysis.OpaqueConstructionCode, tc.typ)
			}
		})
	}
}

// TestOpaqueConversion_AdmittedWhereConstructionIs: the provided
// conversions of an opaque type are callable in its own file (the module's
// functions) and in its partner test; a `try_from` the type's file writes
// itself is its public function; and a validated type that is not opaque
// keeps its `try_from` public.
func TestOpaqueConversion_AdmittedWhereConstructionIs(t *testing.T) {
	errs := checkConversions(t, "main", map[string]string{
		"main": `import ids.{self, Pct, Kelvin, Celsius, Open}

fn main()
    dbg ids.pct(5)
    dbg ids.uid(5)
    dbg ids.clamped(500)
    dbg ids.span(1, 2)
    dbg Kelvin.try_from(Celsius(20.0))
    dbg Open.try_from(5)
    f = Open.try_from
    dbg f(5)
    q: Option<Open> = TryFrom.try_from(7)
    dbg q
    p: Option<Pct> = ids.pct(1)
    dbg p
end
`,
	})
	if len(errs) > 0 {
		t.Fatalf("the conversions are refused where they are allowed:%s", diagList(errs))
	}
	errs = checkConversions(t, "ids_test", map[string]string{
		"ids_test": `import ids.{Uid, Pct, Span}

test "the partner test builds its partner's opaque types"
    assert Uid.try_from(5) != None
    assert Pct.try_from(5) != None
    assert Pct.unchecked(5) == Pct(5)
    assert Span.try_from({low: 1, high: 2}) != None
    f = Pct.try_from
    assert f(200) == None
end
`,
	})
	if len(errs) > 0 {
		t.Fatalf("the partner test may not build its partner's opaque types:%s", diagList(errs))
	}
}

// genericModule makes a value through a bound on `TryFrom`, and holds an
// impl whose `where` needs it.
const genericModule = `import ids.Celsius

pub fn make<T>(n: Int) -> Option<T> where T: TryFrom<Int>
    T.try_from(n)
end

pub fn from_float<T>(x: Float) -> Option<T> where T: TryFrom<Float>
    T.try_from(x)
end

pub fn from_celsius<T>(c: Celsius) -> Option<T> where T: TryFrom<Celsius>
    T.try_from(c)
end

pub interface Maker
    fn build(x: self, n: Int) -> Int
end

pub struct Box<T>
    v: T
end

impl Maker for Box<T> where T: TryFrom<Int>
    fn build(_box: Box<T>, n: Int) -> Int
        if T.try_from(n) is Some(_) then 1 else 0
    end
end
`

// TestOpaqueConversion_ProvidedImplMeetsNoBoundOutside: outside an opaque
// type's file, the `TryFrom` impl the compiler provides for it does not
// count toward a bound, at a generic call, an explicit type argument, a
// generic function value, an impl's `where`, a conversion to a `TryFrom`
// value and a call through `TryFrom`. Each is the missing-impl error with
// the reason, coded as opaque construction. A `TryFrom` impl the type's
// file writes itself meets a bound everywhere.
func TestOpaqueConversion_ProvidedImplMeetsNoBoundOutside(t *testing.T) {
	bound := func(typ, iface string) string {
		return fmt.Sprintf("%s does not implement %s (required by `where T: %s`)", typ, iface, iface)
	}
	cases := []struct {
		name, body, typ, want string
		col                   int
	}{
		{"a bound the expected type solves", "    q: Option<Pct> = gen.make(5)\n    dbg q\n", "Pct", bound("Pct", "TryFrom<Int>"), 22},
		{"an explicit type argument", "    dbg gen.make<Uid>(5)\n", "Uid", bound("Uid", "TryFrom<Int>"), 9},
		{"a std type", "    dbg gen.make<Codepoint>(65)\n", "Codepoint", bound("Codepoint", "TryFrom<Int>"), 9},
		{"a generic function value", "    f: (Int) -> Option<Pct> = gen.make\n    dbg f(5)\n", "Pct", bound("Pct", "TryFrom<Int>"), 34},
		{"a type with a TryFrom of its own", "    dbg gen.from_float<Kelvin>(1.0)\n", "Kelvin", bound("Kelvin", "TryFrom<Float>"), 9},
		{"an impl's where", "    dbg gen.Maker.build(gen.Box{v: ids.pct(5)!}, 7)\n", "Pct", "no impl of `TryFrom` for `Pct` (required by the call `gen.Maker.build`)", 24},
		{"a TryFrom value", "    dbg take(ids.pct(5)!)\n", "Pct", "no impl of `TryFrom` for `Pct` (required by parameter `x` of `take`)", 21},
		{"a call through TryFrom", "    q: Option<Pct> = TryFrom.try_from(7)\n    dbg q\n", "Pct", "no impl of `TryFrom` for `Pct` (required by the call `TryFrom.try_from`)", 38},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkConversions(t, "main", map[string]string{
				"gen":  genericModule,
				"main": "import\n    ids.{self, Uid, Pct, Kelvin}\n    gen\nend\n\nfn take(x: TryFrom<Int>) -> Int\n    dbg x\n    1\nend\n\nfn main()\n" + tc.body + "end\n",
			})
			var found, others []analysis.TypeError
			for _, e := range errs {
				switch {
				case e.Message == tc.want:
					found = append(found, e)
				case e.Code != analysis.UnusedImportCode:
					// Each case imports what every case uses.
					others = append(others, e)
				}
			}
			if len(found) != 1 || len(others) != 0 {
				t.Fatalf("want the one error %q, got:%s", tc.want, diagList(errs))
			}
			e := found[0]
			if e.Line != 12 || e.Col != tc.col || e.Code != analysis.OpaqueConstructionCode || len(e.Args) != 1 || e.Args[0] != tc.typ {
				t.Fatalf("the error is at %d:%d with code %q %v, want 12:%d with %q [%s]", e.Line, e.Col, e.Code, e.Args, tc.col, analysis.OpaqueConstructionCode, tc.typ)
			}
			if len(e.Hints) == 0 || !strings.Contains(e.Hints[len(e.Hints)-1], "'"+tc.typ+"' is opaque, and its construction is private to its defining module") {
				t.Fatalf("the error's help %q does not say why", e.Hints)
			}
		})
	}
}

// TestOpaqueConversion_ProvidedImplMeetsBoundsInside: in an opaque type's
// file and its partner test, the `TryFrom` impl the compiler provides for
// it meets a bound, a generic one another file declares included; and a
// `TryFrom` impl the type's file writes itself meets a bound outside.
func TestOpaqueConversion_ProvidedImplMeetsBoundsInside(t *testing.T) {
	inside := conversionsModule + `
pub fn made(n: Int) -> Option<Pct>
    gen.make(n)
end

pub fn built(n: Int) -> Int
    gen.Maker.build(gen.Box{v: Pct(5)}, n)
end
`
	p := newManager().CheckProgram("/nomi-opaque-conversions", "main", map[string]string{
		"ids": "import gen\n\n" + inside,
		"gen": genericModule,
		"main": `import
    ids.{self, Celsius, Kelvin}
    gen
end

fn main()
    dbg ids.made(5)
    dbg ids.built(5)
    k: Option<Kelvin> = gen.from_celsius(Celsius(20.0))
    dbg k
end
`,
		"ids_test": `import
    ids.{Pct, Uid}
    gen
end

test "the partner test meets bounds through the provided impls"
    assert gen.make<Pct>(5) != None
    assert gen.make<Uid>(0) == None
end
`,
	})
	var errs []analysis.TypeError
	for _, key := range p.Keys() {
		errs = append(errs, p.Errors[key]...)
	}
	if len(errs) > 0 {
		t.Fatalf("the provided impls are refused where construction is allowed:%s", diagList(errs))
	}
}
