package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// std/bool declares the host types `True` and `False` that Bool embeds.
// Imported by their own names (`import std::bool.{False, True}`), as a std
// file may import them, their names as values are Bool's variants, as the
// prelude's and `bool.Bool.{False, True}`'s are: an `if` condition, two
// branches, a tuple item and an annotated binding take them as Bools. They
// were values of the host types, so the condition was the error "if
// condition must be Bool, got True", the branches "then is True, else is
// False", and the tuple, which the checker widened, declined in the IR
// builder ("a value of kind Bool where True is expected").
func TestBoolImport_TheTypeNamesAreTheVariants(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "bool_import_test.nomi"
	p, err := vmhost.LoadSource(name, `import std::bool.{False, True}

fn pick(n: Int) -> Bool
    if n > 0 then True else False
end

fn step(xs: List<Int>, flag: Bool) -> (List<Int>, Bool)
    case xs
        [_, ..rest] when flag -> (rest, flag)
        _                     -> (xs, True)
    end
end

test "the imported names are Bool's variants"
    flag = True
    assert flag
    assert pick(1)
    refute pick(0)

    pair: (List<Int>, Bool) = ([], False)
    assert pair == ([], 1 == 2)
    assert step([1], False) == ([1], 1 == 1)
end
`)
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	out := buf.String()
	if strings.Contains(out, "BLOCKED") || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("want one passing case:\n%s", out)
	}
}
