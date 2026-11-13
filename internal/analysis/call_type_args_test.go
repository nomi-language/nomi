package analysis

import (
	"testing"
)

const callTypeArgsSrc = `interface Source<T>
    fn first(s: self) -> T
end

struct Words
    w: String
end

impl Source<String> for Words
    fn first(s: Words) -> String
        s.w
    end
end

fn has_any<T, I>(it: I) -> Int where I: Source<T>
    _ = Source.first(it)
    1
end

fn pick<A, B>(a: A, b: B) -> (B, A)
    (b, a)
end

fn run() -> Int
    (_, n) = pick(1, "x")
    n + has_any(Words{w: "a"})
end
`

// TestCallTypeArgs_ReadsEveryParameter: each callee type parameter has its
// argument in the record, one the signature shows (pick's A and B) and one
// only a where bound names (has_any's T, which the result does not name
// either), which the checker solves from the impl the bound's subject has
// and records with the call's substitution.
func TestCallTypeArgs_ReadsEveryParameter(t *testing.T) {
	fa, nodes := checkNodes(t, callTypeArgsSrc)
	want := map[string]map[string]string{
		"has_any": {"T": "String", "I": "Words"},
		"pick":    {"A": "Int", "B": "String"},
	}
	for name, args := range want {
		calls := elabCalls(nodes, name)
		if len(calls) != 1 {
			t.Fatalf("want one call of %s, got %d", name, len(calls))
		}
		pos, _ := calleeKey(calls[0].Func)
		got := CallTypeArgs(fa.References[pos])
		if len(got) != len(args) {
			t.Errorf("%s: want %d type arguments, got %v", name, len(args), got)
		}
		for p, ty := range args {
			if got[p] == nil || got[p].String() != ty {
				t.Errorf("%s: %s = %v, want %s", name, p, got[p], ty)
			}
		}
	}
	if got := VerifyElaboration(fa, nodes); len(got) > 0 {
		t.Fatalf("want nothing reported, got %v", got)
	}
}

const argsFirstSrc = `interface Shown
    fn shown(x: self) -> Int
end

impl Shown for Int
    fn shown(x: Int) -> Int
        x
    end
end

fn step<T>(from: T, f: (T) -> T) -> T
    f(from)
end

fn run() -> Int
    s: Shown = step(1, fn n -> n + 1)
    Shown.shown(s)
end
`

// TestCallTypeArgs_TheArgumentsChooseTheInstance: a generic call whose
// result is expected to be an interface is instantiated at the type its
// arguments give (step at Int, so its lambda adds Ints), and the result
// enters the interface by a Coercion record at the call, which the IR
// builder reads (spec §5 *Type arguments from the arguments*).
func TestCallTypeArgs_TheArgumentsChooseTheInstance(t *testing.T) {
	fa, nodes := checkNodes(t, argsFirstSrc)
	calls := elabCalls(nodes, "step")
	if len(calls) != 1 {
		t.Fatalf("want one call of step, got %d", len(calls))
	}
	pos, _ := calleeKey(calls[0].Func)
	got := CallTypeArgs(fa.References[pos])
	if got["T"] == nil || got["T"].String() != "Int" {
		t.Errorf("step's T = %v, want Int", got["T"])
	}
	co := fa.Coercions[calls[0]]
	if co == nil || co.Iface.Name != "Shown" || co.Value.String() != "Int" {
		t.Errorf("the call's coercion = %+v, want Int into Shown", co)
	}
	if got := VerifyElaboration(fa, nodes); len(got) > 0 {
		t.Fatalf("want nothing reported, got %v", got)
	}
}
