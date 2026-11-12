package irbuild

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

func TestIRPreludeValues_DeclaringIdentity(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", "fn some() -> Maybe<Int>\n    Some(42)\nend\nfn none() -> Maybe<Int>\n    None\nend\nfn ok() -> Result<Int, String>\n    Ok(7)\nend\nfn err() -> Result<Int, String>\n    Err(\"bad\")\nend")
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"some": "maybe.Maybe", "none": "maybe.Maybe", "ok": "results.Result", "err": "results.Result"}
	for _, mod := range res.IR {
		for _, fn := range mod.Funcs() {
			name, found := want[fn.Name()]
			if !found {
				continue
			}
			got, err := vmRunSymV(vm.NewProgram(mod, res.IRModules(), io.Discard), fn.Sym())
			if err != nil {
				t.Fatal(err)
			}
			_, enum, _, ok := vmRecord(got)
			if !ok || enum != name {
				t.Fatalf("%s: identity %#v, want %s", fn.Name(), got, name)
			}
			delete(want, fn.Name())
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing retained functions: %v", want)
	}
}

func TestIRPreludeValues_MissingCheckedSignatureKeepsNativeFallback(t *testing.T) {
	// No expected type reaches line 3's constructor: an unannotated
	// binding. An expected instance of the same enum would type it, which is
	// not metadata the checker lost.
	p, err := AnalyzeSource("main.nomi", "import std::io\nfn make() -> Int\n    x = Some(42)\n read(x)\nend\nfn read(v: Maybe<Int>) -> Int\n    case v\n        Some(n) -> n\n        None -> 0\n    end\nend\nfn main()\n    io.print(make())\nend")
	if err != nil {
		t.Fatal(err)
	}
	cleared := 0
	for pos, sym := range p.Entry().FA.References {
		if pos.Line == 3 && sym.Name == "Some" && sym.CallType != nil {
			sym.CallType = nil
			cleared++
		}
	}
	if cleared != 1 {
		t.Fatalf("cleared %d constructor signatures", cleared)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range res.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() == "make" {
				t.Fatal("constructor without metadata retained")
			}
		}
	}
}

func TestIRPreludeValues_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"cached stdlib refinement", `import
    std::io std::int.PositiveInt
end
fn present(v: Maybe<PositiveInt>) -> Bool
    case v
        Some(_) -> True
        None -> False
    end
end
fn main()
  positive = Int.to_positive
  io.inspect(present(positive(5)))
  io.inspect(present(positive(0)))
end`, "True\nFalse\n"},
		{"anchored Fragment constructors", `import
    std::io std::literals.Fragment
end
fn text(f: Fragment<Int>) -> String
  case f
    Fragment.Static(s) -> s
    Fragment.Dynamic(n) -> "number ${n}"
  end
end
fn main()
  io.print(text(Fragment.Static<Int>("literal")))
  io.print(text(Fragment.Dynamic(42)))
end`, "literal\nnumber 42\n"},
		{"qualified and aliased values", `import
    std::io std::maybe.Maybe.{Some as Just, None as Nothing}
end
fn choose(flag: Bool) -> Maybe<Int>
    if flag then Just(42) else Nothing
end
fn read(v: Maybe<Int>) -> Int
    case v
        Some(n) -> n
        None -> 0
    end
end
fn empty() -> Maybe<Int>
    Maybe.None
end
fn main()
  io.print(read(choose(True)))
  io.print(read(choose(False)))
  io.print(read(Maybe.Some(7)))
  io.print(read(empty()))
end`, "42\n0\n7\n0\n"},
		{"argument and default context", `import std::io
fn read(v: Maybe<Int> = None) -> Int
    case v
        Some(n) -> n
        None -> 0
    end
end
fn main()
    io.print(read(None))
    io.print(read())
    io.print(read(Some(42)))
end`, "0\n0\n42\n"},
		{"effects and generic instances", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    return n
end
fn wrap<T>(x: T) -> Maybe<T>
    Some(x)
end
fn number(v: Maybe<Int>) -> Int
    case v
        Some(n) -> n
        None -> 0
    end
end
fn text(v: Maybe<String>) -> String
    case v
        Some(s) -> s
        None -> "empty"
    end
end
fn main()
  io.print(number(wrap(mark(42))))
  io.print(text(wrap("yes")))
end`, "42\n42\nyes\n"},
		{"Maybe cases", `import std::io
fn lookup(id: Int) -> Maybe<String>
  case id
    1 -> Some("Alice")
    _ -> None
  end
end
fn name_for(id: Int) -> String
  case lookup(id)
    Some(name) -> name
    None -> "unknown"
  end
end
fn main()
    io.print(name_for(1))
    io.print(name_for(99))
end`, "Alice\nunknown\n"},
		{"Result cases", `import std::io
fn calculate(ok: Bool) -> Result<Int, String>
  if ok then Ok(42) else Err("bad")
end
fn show(ok: Bool) -> String
  case calculate(ok)
    Ok(n) -> "value ${n}"
    Err(e) -> e
  end
end
fn main()
    io.print(show(True))
    io.print(show(False))
end`, "value 42\nbad\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
