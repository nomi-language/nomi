package vmhost_test

import "testing"

// A name a body binds shadows an import of that name, as a local, a
// parameter, a pattern's name, a case arm's name and a lambda's parameter:
// `testing.check(x)` and `io.print(s)` over it call the value's fields, and
// a function of its own reaches the imported modules.
func TestRun_ABoundNameShadowingAnImportCallsItsField(t *testing.T) {
	out := runOutput(t, `import
    std::io
    std::testing
end

struct Checker
    check: (Bool) -> Bool
    print: (String) -> Int
end

fn yes(_b: Bool) -> Bool
    True
end

fn size(s: String) -> Int
    String.length(s)
end

fn by_local(made: Checker) -> Int
    testing = made
    io = made
    b: Bool = testing.check(False)
    if b then io.print("a") else 0
end

fn by_param(testing: Checker, io: Checker) -> Int
    b: Bool = testing.check(False)
    if b then io.print("ab") else 0
end

fn by_pattern(made: Checker) -> Int
    (testing, io) = (made, made)
    b: Bool = testing.check(False)
    if b then io.print("abc") else 0
end

fn by_case(made: Checker) -> Int
    case (made, made)
        (testing, io) ->
            b: Bool = testing.check(False)
            if b then io.print("abcd") else 0
    end
end

fn by_lambda(made: Checker) -> Int
    f = fn testing: Checker, io: Checker ->
        b: Bool = testing.check(False)
        if b then io.print("abcde") else 0
    end
    f(made, made)
end

fn main()
    made = Checker{check: yes, print: size}
    io.print("${by_local(made)} ${by_param(made, made)} ${by_pattern(made)} ${by_case(made)} ${by_lambda(made)}")
    io.print("${testing.check(False) is Err(_)}")
end
`)
	if want := "1 2 3 4 5\nTrue\n"; out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}
