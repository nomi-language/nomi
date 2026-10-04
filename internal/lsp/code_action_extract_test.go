package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const (
	extract = protocol.CodeActionKindRefactorExtract
	inline  = protocol.CodeActionKindRefactorInline
)

func TestExtractVariable_Selection(t *testing.T) {
	src := fnMain("xs = [1, 2, 3]\nio.print(\"${«Iter.count(xs)» + 1}\")\n")
	got := checkRefactor(t, src, "Extract variable 'count'", extract)
	if want := fnMain("xs = [1, 2, 3]\ncount = Iter.count(xs)\nio.print(\"${count + 1}\")\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestExtractVariable_CursorTakesInnermostCall(t *testing.T) {
	src := fnMain("xs = [1, 2, 3]\nio.inspect(Iter.to_list(Iter.map(xs, |x| x * 2)) == [2, 4, 6])\n")
	src = fnMain("xs = [1, 2, 3]\nio.inspect(Iter.to_li‸st(Iter.map(xs, |x| x * 2)) == [2, 4, 6])\n")
	got := checkRefactor(t, src, "Extract variable 'list'", extract)
	if want := fnMain("xs = [1, 2, 3]\nlist = Iter.to_list(Iter.map(xs, |x| x * 2))\nio.inspect(list == [2, 4, 6])\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestExtractVariable_FieldNameAndCollision(t *testing.T) {
	decls := "struct User {\n    name: String\n}\n\n"
	src := fnMainAfter(decls, "name = \"x\"\nu = User{name: \"a\"}\nio.print(\"${«u.name»}${name}\")\n")
	got := checkRefactor(t, src, "Extract variable 'name2'", extract)
	if want := fnMainAfter(decls, "name = \"x\"\nu = User{name: \"a\"}\nname2 = u.name\nio.print(\"${name2}${name}\")\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestExtractVariable_InsideLambdaBlock(t *testing.T) {
	src := fnMain("n =\n    [1, 2]\n    |> Iter.map(|x| {\n        io.print(\"${x}\")\n        Int.to_str‸ing(x)\n    })\n    |> Iter.to_list()\n\nio.inspect(n)\n")
	got := checkRefactor(t, src, "Extract variable 'string'", extract)
	want := fnMain("n =\n    [1, 2]\n    |> Iter.map(|x| {\n        io.print(\"${x}\")\n        string = Int.to_string(x)\n        string\n    })\n    |> Iter.to_list()\n\nio.inspect(n)\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// A call goes before the statement when nothing in it runs earlier.
func TestExtractVariable_NoEarlierEffect(t *testing.T) {
	src := fnMain("io.print(\"${Int.to_str‸ing(1)}${io.read_line()}\")\n")
	got := checkRefactor(t, src, "Extract variable 'string'", extract)
	if want := fnMain("string = Int.to_string(1)\nio.print(\"${string}${io.read_line()}\")\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestExtractVariable_Refused(t *testing.T) {
	for name, body := range map[string]string{
		// Out of a lambda's body.
		"lambda body": "n = Iter.map([1], |x| Int.to_str‸ing(x))\nio.inspect(Iter.to_list(n))\n",
		// A case arm runs only when its pattern matches.
		"case arm": "m = String.to_int(\"1\")\nn = case m {\n    Some(v) -> Int.to_str‸ing(v)\n    None -> \"\"\n}\n\nio.inspect(n)\n",
		// The right side of `and` runs only when the left is true.
		"and": "xs = [1]\nok = Iter.empty?(xs) and Iter.count‸(xs) > 0\nio.inspect(ok)\n",
		// A pipe stage is not a value.
		"pipe stage": "n = [1] |> Iter.co‸unt()\n\nio.inspect(n)\n",
		// An earlier call in the statement would run after it.
		"earlier effect": "io.print(\"${io.read_line()}${Int.to_str‸ing(1)}\")\n",
		// The whole value of a binding.
		"binding value": "n = Iter.count‸([1])\nio.inspect(n)\n",
		// A `.Variant` needs the type expected of it.
		"dot variant": "m: Maybe<Int> = «.None»\nio.inspect(m)\n",
	} {
		t.Run(name, func(t *testing.T) {
			refuseRefactor(t, fnMain(body), "Extract variable")
		})
	}
}

func TestInlineVariable(t *testing.T) {
	src := fnMain("xs = [1, 2, 3]\nn‸ = Iter.count(xs)\nio.inspect(n + 1)\n")
	got := checkRefactor(t, src, "Inline variable 'n'", inline)
	if want := fnMain("xs = [1, 2, 3]\nio.inspect(Iter.count(xs) + 1)\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// From the read, and with the parentheses an operator needs.
	src = fnMain("a = 2\nb = a + 1\nio.inspect(b‸ * 3)\n")
	got = checkRefactor(t, src, "Inline variable 'b'", inline)
	if want := fnMain("a = 2\nio.inspect((a + 1) * 3)\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// A pure value goes anywhere its names mean the same, a lambda body too.
	src = fnMain("k‸ = 2\nn = Iter.map([1], |x| x * k)\nio.inspect(Iter.to_list(n))\n")
	got = checkRefactor(t, src, "Inline variable 'k'", inline)
	if want := fnMain("n = Iter.map([1], |x| x * 2)\nio.inspect(Iter.to_list(n))\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestInlineVariable_Refused(t *testing.T) {
	for name, body := range map[string]string{
		"two reads": "n‸ = Iter.count([1])\nio.inspect(n + n)\n",
		// A call moved past another statement would run after it.
		"not next": "n‸ = Iter.count([1])\nio.print(\"a\")\nio.inspect(n)\n",
		// A call moved into a lambda would run once per element.
		"into lambda": "k‸ = Iter.count([1])\nm = Iter.map([1], |x| x * k)\nio.inspect(Iter.to_list(m))\n",
		// The name the value reads is rebound before the read.
		"shadowed": "a = 1\nb‸ = a + 1\na = 5\nio.inspect(a + b)\n",
		// An annotation gives the value its type.
		"annotated": "m‸: Maybe<Int> = .None\nio.inspect(m)\n",
	} {
		t.Run(name, func(t *testing.T) {
			refuseRefactor(t, fnMain(body), "Inline variable")
		})
	}
}
