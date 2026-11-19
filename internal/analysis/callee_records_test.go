package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// callsByLine is the record of each call fa checked, by the line it is on.
func callsByLine(fa *analysis.FileAnalysis) map[int]analysis.Callee {
	out := map[int]analysis.Callee{}
	for n, rc := range fa.Calls {
		if call, ok := n.(*ast.Call); ok {
			out[call.Line] = rc.Callee
		}
	}
	return out
}

// atTypeArgs is c, a record in a generic body, at the type arguments the
// call use records for its generic callee.
func atTypeArgs(fa *analysis.FileAnalysis, c, use analysis.Callee) analysis.Callee {
	args := use.Ref.CallTypeArgs
	return fa.CalleeAt(c, func(p *analysis.TypeParam_) (analysis.Type, bool) {
		ty, ok := args[p.Name_]
		return ty, ok && ty != nil
	})
}

// wantImplOf fails unless c reaches the impl block for recv, with the
// instance's type as For.
func wantImplOf(t *testing.T, what string, c analysis.Callee, recv string) *ast.ImplBlock {
	t.Helper()
	if c.Kind != analysis.CalleeImpl {
		t.Fatalf("%s: kind %d, want CalleeImpl (%+v)", what, c.Kind, c)
	}
	b, ok := c.Impl.(*ast.ImplBlock)
	if !ok {
		t.Fatalf("%s: Impl %T, want the impl block", what, c.Impl)
	}
	if b.Receiver.TypeString() != recv {
		t.Errorf("%s: the block is for %s, want %s", what, b.Receiver.TypeString(), recv)
	}
	if c.For == nil || analysis.ContainsTypeParam(c.For) {
		t.Errorf("%s: For %v, want the instance's type", what, c.For)
	}
	return b
}

// A membership test `x in c` is the call `Contains.contains(c, x)`, and its
// record is that call's: Contains's impl for the collection's type, in a
// generic body too (`List<T>`, as `Display.to_string(xs)` there records
// List's Display impl), which an instance reads at its own types.
func TestCallee_MembershipIsACallOfContains(t *testing.T) {
	fa, errs := checkEntry(t, `fn has(xs: List<Int>) -> Bool
    3 in xs
end

fn has_written(xs: List<Int>) -> Bool
    Contains.contains(xs, 3)
end

fn holds<T>(xs: List<T>, x: T) -> Bool where T: Equatable
    x in xs
end

fn holds_int(xs: List<Int>) -> Bool
    holds(xs, 3)
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	byLine := callsByLine(fa)
	in, written := byLine[2], byLine[6]
	wantImplOf(t, "3 in xs", in, "List<T>")
	if in.Decl != written.Decl || in.Impl != written.Impl || in.For.String() != written.For.String() {
		t.Errorf("`3 in xs` records %+v, want the record of Contains.contains(xs, 3): %+v", in, written)
	}
	if in.Qualifier.Kind != analysis.QualInterface || in.Method != "contains" {
		t.Errorf("`3 in xs`: qualifier %+v method %q, want Contains.contains", in.Qualifier, in.Method)
	}
	// In a generic body the collection's type holds T, so each instance
	// answers which impl the call reaches, as `xs == ys` there is.
	generic := byLine[10]
	if generic.Kind != analysis.CalleeInterface || generic.Impl != nil || generic.Form != analysis.OperatorPerInstance || generic.For == nil {
		t.Fatalf("`x in xs` in a generic body: %+v, want Contains's function, per instance", generic)
	}
	if got := generic.For.String(); got != "List<T>" {
		t.Errorf("`x in xs` in a generic body: For %s, want List<T>", got)
	}
	if at := atTypeArgs(fa, generic, byLine[14]); at.Impl != in.Impl {
		t.Errorf("`x in xs` at T = Int reaches another function than `3 in xs`")
	}
}

// A type parameter a generic receiver names without a `<T>` clause is a
// type parameter to the record of a call it qualifies, as a function's own
// and a block's written one are: in an interface's impl block (`impl Make
// for Box<A>`) and in the type's own (`impl Box<A>`), the qualifier is the
// parameter and the call is for it, so an instance resolves it.
func TestCallee_ImplBlockTypeParamQualifies(t *testing.T) {
	fa, errs := checkEntry(t, `interface Make
    fn make() -> self
end

impl Make for Int
    fn make() -> Int
        1
    end
end

struct Box<A>
    v: A
end

impl Make for Box<A> where A: Make
    fn make() -> Box<A>
        Box{v: A.make()}
    end
end

impl Box<A>
    fn fresh() -> A where A: Make
        A.make()
    end
end

impl<B> Make for List<B> where B: Make
    fn make() -> List<B>
        [B.make()]
    end
end

fn made<T>() -> T where T: Make
    T.make()
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	byLine := callsByLine(fa)
	atInt := func(p *analysis.TypeParam_) (analysis.Type, bool) {
		return analysis.TypeInt, true
	}
	for _, c := range []struct {
		what  string
		line  int
		param string
	}{
		{"A.make() in impl Make for Box<A>", 17, "A"},
		{"A.make() in impl Box<A>", 23, "A"},
		{"B.make() in impl<B> Make for List<B>", 29, "B"},
		{"T.make() in fn made<T>", 34, "T"},
	} {
		rec := byLine[c.line]
		if rec.Qualifier.Kind != analysis.QualTypeParam || rec.Qualifier.Param == nil || rec.Qualifier.Param.Name != c.param {
			t.Errorf("%s: qualifier %+v, want the type parameter %s", c.what, rec.Qualifier, c.param)
			continue
		}
		if rec.Kind != analysis.CalleeInterface || rec.For == nil || rec.For.String() != c.param {
			t.Errorf("%s: kind %d For %v, want Make's function for %s", c.what, rec.Kind, rec.For, c.param)
			continue
		}
		at := fa.CalleeAt(rec, atInt)
		wantImplOf(t, c.what+" at Int", at, "Int")
		if fd, ok := at.Decl.(*ast.FuncDef); !ok || fd.Name != "make" || !at.InProgram {
			t.Errorf("%s at Int: Decl %T, want the program's fn make", c.what, at.Decl)
		}
	}
}

// A bare call of an interface's own function inside one of its defaults
// is for the type the default runs for: the default's `self`. At an
// instance that binds `self`, the record is the implementing type's
// function: its written one, or the default it inherits.
func TestCallee_BareCallInADefaultIsForSelf(t *testing.T) {
	fa, errs := checkEntry(t, `interface Greet
    fn name(value: self) -> String

    open fn greet(value: self) -> String
        "hi " + name(value)
    end

    open fn twice(value: self) -> String
        greet(value) + greet(value)
    end
end

struct P
    n: String
end

impl Greet for P
    fn name(p: P) -> String
        p.n
    end
end

struct Q
    n: String
end

impl Greet for Q
    fn name(q: Q) -> String
        q.n
    end

    fn greet(q: Q) -> String
        "hello " + q.n
    end
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	byLine := callsByLine(fa)
	types := map[string]analysis.Type{}
	for _, name := range []string{"P", "Q"} {
		if sym := fa.ModuleScope.Lookup(name); sym != nil {
			types[name] = sym.Type
		}
	}
	at := func(c analysis.Callee, recv string) analysis.Callee {
		return fa.CalleeAt(c, func(p *analysis.TypeParam_) (analysis.Type, bool) {
			return types[recv], p.Name_ == "self" && types[recv] != nil
		})
	}
	name, greet := byLine[5], byLine[9]
	for what, c := range map[string]analysis.Callee{"name(value) in greet": name, "greet(value) in twice": greet} {
		if c.Kind != analysis.CalleeInterface || c.Owner.Kind != analysis.QualInterface || c.Qualifier.Kind != analysis.QualNone {
			t.Fatalf("%s: %+v, want Greet's function, called bare", what, c)
		}
		if p, ok := c.For.(*analysis.TypeParam_); !ok || p.Name_ != "self" {
			t.Errorf("%s: For %v, want the default's self", what, c.For)
		}
	}
	fnNamed := func(c analysis.Callee, what, want string) {
		t.Helper()
		if fd, ok := c.Decl.(*ast.FuncDef); !ok || fd.Name != want || !c.InProgram {
			t.Errorf("%s: Decl %T, want the block's fn %s", what, c.Decl, want)
		}
	}
	fnNamed(wantImplOfAt(t, "name(value) at P", at(name, "P"), "P"), "name(value) at P", "name")
	fnNamed(wantImplOfAt(t, "greet(value) at Q", at(greet, "Q"), "Q"), "greet(value) at Q, which writes it", "greet")
	inherited := wantImplOfAt(t, "greet(value) at P", at(greet, "P"), "P")
	if im, ok := inherited.Decl.(*ast.InterfaceMethod); !ok || im.Name != "greet" {
		t.Errorf("greet(value) at P, which inherits it: Decl %T, want the interface's default", inherited.Decl)
	}
}

// wantImplOfAt is wantImplOf that answers the record.
func wantImplOfAt(t *testing.T, what string, c analysis.Callee, recv string) analysis.Callee {
	t.Helper()
	wantImplOf(t, what, c, recv)
	return c
}

// A record CalleeAt answers at an instance is the record of the function
// that runs there, its std identity included: a std interface's function,
// answered at a program's type, reaches the program's `fn`, which has none.
func TestCalleeAt_AProgramsImplHasNoStdIdentity(t *testing.T) {
	fa, errs := checkEntry(t, `struct P
    n: Int
end

impl Hashable for P
    fn hash(p: P) -> Int
        p.n
    end
end

fn hashed<T>(x: T) -> Int where T: Hashable
    Hashable.hash(x)
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	rec := callsByLine(fa)[12]
	if rec.Kind != analysis.CalleeInterface || rec.Std.IsZero() {
		t.Fatalf("Hashable.hash(x) on T: %+v, want std's interface function", rec)
	}
	p := fa.ModuleScope.Lookup("P").Type
	at := fa.CalleeAt(rec, func(tp *analysis.TypeParam_) (analysis.Type, bool) { return p, tp.Name_ == "T" })
	wantImplOf(t, "Hashable.hash at P", at, "P")
	if !at.InProgram || !at.Std.IsZero() {
		t.Errorf("Hashable.hash at P: InProgram %v Std %+v, want the program's fn with no std identity", at.InProgram, at.Std)
	}
}

// A call through a type parameter of an interface a type implements at
// several type arguments reaches, at an instance, the block of the
// interface instance the call goes through: `C.holds(c, x)` under
// `C: Holds<T>`, and `Holds.holds(b, x)` in a default of `Holds<T>`, at
// T = Int and at T = String.
func TestCalleeAt_SeveralBlocksAtTheCallsInterfaceInstance(t *testing.T) {
	fa, errs := checkEntry(t, `interface Holds<T>
    fn holds(b: self, x: T) -> Bool

    fn holds_none(b: self, xs: List<T>) -> Bool
        Iter.all_match(xs, fn x -> not Holds.holds(b, x))
    end
end

struct Box
    n: Int
    s: String
end

impl Holds<Int> for Box
    fn holds(b: Box, x: Int) -> Bool
        b.n == x
    end
end

impl Holds<String> for Box
    fn holds(b: Box, x: String) -> Bool
        b.s == x
    end
end

fn holds_by<C, T>(c: C, x: T) -> Bool where C: Holds<T>
    C.holds(c, x)
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	holds := map[int]analysis.Callee{}
	for n, rc := range fa.Calls {
		if call, ok := n.(*ast.Call); ok && rc.Callee.Method == "holds" {
			holds[call.Line] = rc.Callee
		}
	}
	box := fa.ModuleScope.Lookup("Box").Type
	for _, arg := range []struct {
		name string
		typ  analysis.Type
		want string
	}{{"Int", analysis.TypeInt, "Int"}, {"String", analysis.TypeString, "String"}} {
		for what, c := range map[string]analysis.Callee{"Holds.holds in the default": holds[5], "C.holds": holds[27]} {
			if c.Kind != analysis.CalleeInterface {
				t.Fatalf("%s: %+v, want Holds's function", what, c)
			}
			at := fa.CalleeAt(c, func(p *analysis.TypeParam_) (analysis.Type, bool) {
				switch p.Name_ {
				case "self", "C":
					return box, true
				case "T":
					return arg.typ, true
				}
				return nil, false
			})
			b := wantImplOf(t, what+" at T = "+arg.name, at, "Box")
			if got := b.Interface.TypeString(); got != "Holds<"+arg.want+">" {
				t.Errorf("%s at T = %s: the block for Holds<%s>, want Holds<%s>", what, arg.name, got, arg.want)
			}
		}
	}
}

// A qualified call of an interface's function on a default's `self`
// (`Greet.name(value)` in Greet's `greet`) is for the default's `self` type
// parameter, the interface's SelfParam, as a bare call there is, not for an
// interface value: at an instance that binds `self`, the record is the
// implementing type's function.
func TestCallee_QualifiedCallOnADefaultsSelfIsForSelf(t *testing.T) {
	fa, errs := checkEntry(t, `interface Greet
    fn name(value: self) -> String

    open fn greet(value: self) -> String
        "hi " + Greet.name(value)
    end

    open fn twice(value: self) -> List<String>
        [Greet.greet(value)]
    end
end

struct P
    n: String
end

impl Greet for P
    fn name(p: P) -> String
        p.n
    end
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	byLine := callsByLine(fa)
	p := fa.ModuleScope.Lookup("P").Type
	for _, line := range []int{5, 9} {
		c := byLine[line]
		if c.Kind != analysis.CalleeInterface || c.Qualifier.Kind != analysis.QualInterface {
			t.Fatalf("line %d: %+v, want Greet's function, qualified by Greet", line, c)
		}
		if tp, ok := c.For.(*analysis.TypeParam_); !ok || tp.Name_ != "self" {
			t.Errorf("line %d: For %v (%T), want the default's self", line, c.For, c.For)
		}
		at := fa.CalleeAt(c, func(tp *analysis.TypeParam_) (analysis.Type, bool) { return p, tp.Name_ == "self" })
		wantImplOf(t, "the call at P", at, "P")
	}
}
