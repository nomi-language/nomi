package irbuild

import (
	"strings"
	"testing"
)

// A generic owner function called where inference left its own type
// argument open (`Box.flat(Box.Full(None))`, flat<T> over `Box<Maybe<T>>`)
// is instantiated at the type the checker closed the variable as
// (analysis.TypeVar.Closed), as its operands are: from the declaring
// file's tests (genericMethodItem) and from another file
// (siblingMethodInstance).
func TestOwnerFnOpenTypeArg_Instantiates(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "True\nTrue\nSome(2)\n"
	got := vmReference(fixture("owner_fn_open_type_arg/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
	tests := vmReference(fixture("owner_fn_open_type_arg/boxes.nomi"))
	if tests.exit != 0 || !strings.Contains(tests.stdout, "2 passed, 0 failed") {
		t.Fatalf("VM test run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s", tests.exit, tests.stdout, tests.stderr)
	}
}

// The same open T under a bound Unit lacks (`where T: Display`): the
// checker closes it at Int, which meets the bound, and the call, its
// operand and the instance are all built at Int.
func TestOwnerFnOpenTypeArg_ABoundedOneInstantiates(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std::io

enum Box<T>
    Full(T)
    Empty
end

impl Box<T>
    fn shown<T>(b: Box<Maybe<T>>) -> String where T: Display
        case b
            .Full(Some(v)) -> Display.to_string(v)
            .Full(None) -> "none"
            .Empty -> "empty"
        end
    end
end

fn main()
    io.print(Box.shown(Box.Full(None)))
    io.print(Box.shown(Box.Full(Some(3))))
end
`, "none\n3\n")
}

// An owner function that uses its block's own T rather than declaring one
// (`fn flat(b: Box<Maybe<T>>) -> Maybe<T>` in `impl Box<T>`) is built in
// the instance of the block the checker instantiated the call at,
// `Box<Int>` for a `Box<Maybe<Int>>` operand, and not in the operand's
// instance (templateCallInstance). It declined before, open T or not.
func TestOwnerFnBlockParam_InstantiatesAtTheCheckersBlock(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std::io

enum Box<T>
    Empty
    Full T
end

impl Box<T>
    fn flat(b: Box<Maybe<T>>) -> Maybe<T>
        case b
            .Full(inner) -> inner
            .Empty -> Maybe.None
        end
    end
end

fn main()
    io.print("${Box.flat(Box.Full(Maybe.None)) == Maybe.None}")
    empty: Box<Maybe<String>> = Box.Empty
    io.print("${Box.flat(empty) == Maybe.None}")
    io.print("${Box.flat(Box.Full(Maybe.Some(2)))}")
end
`, "True\nTrue\nSome(2)\n")
}
