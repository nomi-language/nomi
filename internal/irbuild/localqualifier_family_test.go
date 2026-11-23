package irbuild

import "testing"

// The family of a binding named like something a file declares or imports:
// for each kind of name a binding may share (a std import's alias, a
// sibling file's alias, a module-level function, a `once`), the binding is
// the binding wherever the name means it, and the file's meaning comes back
// where the binding's scope ends. Each program uses the name as a call's
// qualifier, as a field read, as a value passed on, inside a lambda, inside
// a nested function, as a `case` arm's pattern binding, as a lambda's
// parameter and as a function's parameter, then once more after a block
// that bound it has ended.
//
// A binding cannot be named like a type or an interface: a capitalized name
// on the left of `=` is a pattern (TestLocalQualifierFamily_ATypeName...).

// A binding named like a std import (`io`).
func TestLocalQualifierFamily_StdImportAlias(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

struct Printer
    print: (String) -> Unit
end

fn say(s: String)
    io.print("custom: ${s}")
end

fn take(p: Printer)
    p.print("passed as a value")
end

fn qualifier()
    io = Printer{print: say}
    io.print("call qualifier")
end

fn field_read()
    io = Printer{print: say}
    f = io.print
    f("field read")
end

fn as_value()
    io = Printer{print: say}
    take(io)
end

fn in_lambda()
    io = Printer{print: say}
    run = fn -> io.print("inside a lambda")
    run()
end

fn in_nested()
    io = Printer{print: say}
    fn inner()
        io.print("inside a nested function")
    end
    inner()
end

fn in_case(m: Option<Printer>)
    case m
        Some(io) -> io.print("case arm binding")
        None -> io.print("std in the other arm")
    end
end

fn lambda_param()
    run = fn io: Printer -> io.print("lambda parameter")
    run(Printer{print: say})
end

fn parameter(io: Printer)
    io.print("function parameter")
end

fn scoped()
    begin
        io = Printer{print: say}
        io.print("inside the block")
    end
    io.print("std after the block")
end

fn main()
    qualifier()
    field_read()
    as_value()
    in_lambda()
    in_nested()
    in_case(Some(Printer{print: say}))
    in_case(None)
    lambda_param()
    parameter(Printer{print: say})
    scoped()
end
`, `custom: call qualifier
custom: field read
custom: passed as a value
custom: inside a lambda
custom: inside a nested function
custom: case arm binding
std in the other arm
custom: lambda parameter
custom: function parameter
custom: inside the block
std after the block
`)
}

// A binding named like a sibling file's alias (`util`).
func TestLocalQualifierFamily_SiblingFileAlias(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
import util

struct Ops
    twice: (Int) -> Int
end

fn thrice(n: Int) -> Int
    n * 3
end

fn take(o: Ops) -> Int
    o.twice(5)
end

fn qualifier() -> Int
    util = Ops{twice: thrice}
    util.twice(5)
end

fn field_read() -> Int
    util = Ops{twice: thrice}
    f = util.twice
    f(5)
end

fn as_value() -> Int
    util = Ops{twice: thrice}
    take(util)
end

fn in_lambda() -> Int
    util = Ops{twice: thrice}
    run = fn -> util.twice(5)
    run()
end

fn in_nested() -> Int
    util = Ops{twice: thrice}
    fn inner() -> Int
        util.twice(5)
    end
    inner()
end

fn in_case(m: Option<Ops>) -> Int
    case m
        Some(util) -> util.twice(5)
        None -> util.twice(5)
    end
end

fn lambda_param() -> Int
    run = fn util: Ops -> util.twice(5)
    run(Ops{twice: thrice})
end

fn parameter(util: Ops) -> Int
    util.twice(5)
end

fn scoped() -> Int
    inside = begin
        util = Ops{twice: thrice}
        util.twice(5)
    end
    inside + util.twice(5)
end

fn main()
    io.print(qualifier())
    io.print(field_read())
    io.print(as_value())
    io.print(in_lambda())
    io.print(in_nested())
    io.print(in_case(Some(Ops{twice: thrice})))
    io.print(in_case(None))
    io.print(lambda_param())
    io.print(parameter(Ops{twice: thrice}))
    io.print(scoped())
end
`, "15\n15\n15\n15\n15\n15\n10\n15\n15\n25\n", map[string]string{"util.nomi": `pub fn twice(n: Int) -> Int
    n * 2
end
`})
}

// A binding named like a module-level function (`helper`).
func TestLocalQualifierFamily_ModuleFunction(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

struct Runner
    run: (Int) -> Int
end

fn helper() -> Int
    1
end

fn ten(n: Int) -> Int
    n * 10
end

fn take(r: Runner) -> Int
    r.run(2)
end

fn qualifier() -> Int
    helper = Runner{run: ten}
    helper.run(2)
end

fn field_read() -> Int
    helper = Runner{run: ten}
    f = helper.run
    f(2)
end

fn as_value() -> Int
    helper = Runner{run: ten}
    take(helper)
end

fn in_lambda() -> Int
    helper = Runner{run: ten}
    run = fn -> helper.run(2)
    run()
end

fn in_nested() -> Int
    helper = Runner{run: ten}
    fn inner() -> Int
        helper.run(2)
    end
    inner()
end

fn in_case(m: Option<Runner>) -> Int
    case m
        Some(helper) -> helper.run(2)
        None -> helper()
    end
end

fn lambda_param() -> Int
    run = fn helper: Runner -> helper.run(2)
    run(Runner{run: ten})
end

fn parameter(helper: Runner) -> Int
    helper.run(2)
end

fn scoped() -> Int
    inside = begin
        helper = Runner{run: ten}
        helper.run(2)
    end
    inside + helper()
end

fn main()
    io.print(qualifier())
    io.print(field_read())
    io.print(as_value())
    io.print(in_lambda())
    io.print(in_nested())
    io.print(in_case(Some(Runner{run: ten})))
    io.print(in_case(None))
    io.print(lambda_param())
    io.print(parameter(Runner{run: ten}))
    io.print(scoped())
end
`, "20\n20\n20\n20\n20\n20\n1\n20\n20\n21\n")
}

// A binding named like a `once` (`tool`).
func TestLocalQualifierFamily_Once(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

struct Runner
    run: (Int) -> Int
end

fn one(n: Int) -> Int
    n + 1
end

fn ten(n: Int) -> Int
    n * 10
end

once tool = Runner{run: one}

fn take(r: Runner) -> Int
    r.run(2)
end

fn qualifier() -> Int
    tool = Runner{run: ten}
    tool.run(2)
end

fn field_read() -> Int
    tool = Runner{run: ten}
    f = tool.run
    f(2)
end

fn as_value() -> Int
    tool = Runner{run: ten}
    take(tool)
end

fn in_lambda() -> Int
    tool = Runner{run: ten}
    run = fn -> tool.run(2)
    run()
end

fn in_nested() -> Int
    tool = Runner{run: ten}
    fn inner() -> Int
        tool.run(2)
    end
    inner()
end

fn lambda_param() -> Int
    run = fn tool: Runner -> tool.run(2)
    run(Runner{run: ten})
end

fn parameter(tool: Runner) -> Int
    tool.run(2)
end

fn scoped() -> Int
    inside = begin
        tool = Runner{run: ten}
        tool.run(2)
    end
    inside + tool.run(2)
end

fn main()
    io.print(qualifier())
    io.print(field_read())
    io.print(as_value())
    io.print(in_lambda())
    io.print(in_nested())
    io.print(lambda_param())
    io.print(parameter(Runner{run: ten}))
    io.print(scoped())
end
`, "20\n20\n20\n20\n20\n20\n20\n23\n")
}

// A `case` arm's pattern cannot bind a `once`'s name: the checker reads it
// as a binding that matches every value where a comparison was likely
// meant, and rejects it.
func TestLocalQualifierFamily_APatternCannotBindAOncesName(t *testing.T) {
	_, err := AnalyzeSource("entry.nomi", `import std::io

struct Runner
    run: (Int) -> Int
end

fn one(n: Int) -> Int
    n + 1
end

once tool = Runner{run: one}

fn in_case(m: Option<Runner>) -> Int
    case m
        Some(tool) -> tool.run(2)
        None -> tool.run(2)
    end
end

fn main()
    io.print(in_case(None))
end
`)
	requireFrontEndRejection(t, err, "a case arm binding a once's name", "`tool` is a `once`")
}

// A capitalized name on the left of `=` is a pattern, so no binding takes
// a type's or an interface's name: the checker reads `String = ...` and
// `Shape = ...` as enum patterns and rejects them.
func TestLocalQualifierFamily_ATypeOrInterfaceNameIsNoBinding(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"a type", `import std::io

struct Printer
    print: (String) -> Unit
end

fn main()
    String = Printer{print: io.print}
    String.print("hi")
end
`},
		{"an interface", `import std::io

interface Shape
    fn area(value: self) -> Int
end

struct Printer
    print: (String) -> Unit
end

fn main()
    Shape = Printer{print: io.print}
    Shape.print("hi")
end
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AnalyzeSource("entry.nomi", tc.src)
			requireFrontEndRejection(t, err, "a binding named like "+tc.name, "enum pattern requires an enum type")
		})
	}
}
