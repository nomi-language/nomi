package irbuild

import (
	"testing"
)

// The Tour's first app-boot program and focused neighbours run through the
// VM with the expected output, and the retained bodies
// read back to the walk's Sources.
func TestIRAppBoot_ProgramsBootThroughTheVM(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
		retained        []string
	}{
		{"app-fields.md:L13", `import
  std::io
end

struct App
  port: Int
  context: Context
end

fn boot() -> Result<App, String>
  Ok(App{port: 8080, context: Context.root()})
end

fn main()
  io.print("listening on :${App.port}")
end
`, "listening on :8080\n", []string{"boot", "main"}},
		{"app-fields.md:L80", `import
  std::io
end

interface Logger
  fn log(logger: self, msg: String) -> Unit
end

type Stdout
impl Logger for Stdout
  fn log(_logger: Stdout, msg: String) -> Unit
    io.print("[log] ${msg}")
  end
end

struct PrefixedLogger
    tag: String
end
impl Logger for PrefixedLogger
  fn log(logger: PrefixedLogger, msg: String) -> Unit
    io.print("[${logger.tag}] ${msg}")
  end
end

struct App
  logger: Logger
  context: Context
end

fn boot() -> Result<App, String>
  Ok(App{logger: Stdout, context: Context.root()})
end

fn main()
  greet("World")
  audit_greet("Alice")
  greet("Bob")
end

fn audit_greet(name: String)
  with App.logger = PrefixedLogger{tag: "audit"}
  greet(name)
end

fn greet(name: String)
  Logger.log(App.logger, "hello, ${name}")
end
`, "[log] hello, World\n[audit] hello, Alice\n[log] hello, Bob\n",
			[]string{"boot", "main", "audit_greet", "greet", "Stdout.log", "PrefixedLogger.log"}},
		{"field reads in a helper", `import std::io

struct App
  name: String
  port: Int
  context: Context
end

fn base() -> Int
    8000
end

fn boot() -> Result<App, String>
  Ok(App{name: "api", port: base() + 80, context: Context.root()})
end

fn address() -> String
    "${App.name}:${App.port}"
end

fn main()
  io.print(address())
  io.print(App.port + 1)
end
`, "api:8080\n8081\n", []string{"boot", "address", "main"}},
		{"a struct-typed field", `import std::io

struct Config
  host: String
  port: Int
end

struct App
  config: Config
  context: Context
end

fn boot() -> Result<App, String>
  Ok(App{config: Config{host: "localhost", port: 5432}, context: Context.root()})
end

fn main()
  c = App.config
  io.print("${c.host}:${c.port}")
end
`, "localhost:5432\n", []string{"boot", "main"}},
		{"a scoped write restored after the function returns", `import std::io

struct App
  level: Int
  label: String
  context: Context
end

fn boot() -> Result<App, String>
  Ok(App{level: 1, label: "base", context: Context.root()})
end

fn show() -> String
    "${App.label}@${App.level}"
end

fn louder() -> String
  with App.level = App.level + 10
  with App.label = String.lowercase("LOUD")
  io.print(App.level)
  io.print(show())
  deeper()
end

fn deeper() -> String
  with App.level = App.level * 2
  show()
end

fn main()
  io.print(show())
  io.print(louder())
  io.print(show())
end
`, "base@1\n11\nloud@11\nloud@22\nbase@1\n", []string{"boot", "show", "louder", "deeper", "main"}},
		{"an erased field bound, then dispatched", `import std::io

interface Shape
  fn area(shape: self) -> Int
end

struct Square
    side: Int
end
impl Shape for Square
  fn area(square: Square) -> Int
      square.side * square.side
  end
end

struct Rect
    w: Int
    h: Int
end
impl Shape for Rect
  fn area(rect: Rect) -> Int
      rect.w * rect.h
  end
end

struct App
  shape: Shape
  context: Context
end

fn boot() -> Result<App, String>
  Ok(App{shape: Square{side: 3}, context: Context.root()})
end

fn report(label: String) -> Unit
  s = App.shape
  io.print("${label}: ${Shape.area(s)}")
end

fn wide() -> Unit
  with App.shape = Rect{w: 2, h: 5}
  report("rect")
end

fn main()
  report("square")
  wide()
  report("square again")
end
`, "square: 9\nrect: 10\nsquare again: 9\n", []string{"boot", "report", "wide", "main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifyDeferProgram(t, tc.src, tc.want, tc.retained...)
		})
	}
}

// An override beside a defer or inside a branching body retains and runs.
// The deferred call reads the app as it was when the `defer` statement ran:
// `done 8080`, not the 9090 the `with` installs after it.
func TestIRAppBoot_DeferAndBranchRun(t *testing.T) {
	verifyDeferProgram(t, `import std::io

struct Ports
  port: Int
  execution: Context
end

fn boot() -> Result<Ports, String>
  Ok(Ports{port: 8080, execution: Context.root()})
end

fn note(s: String) -> Unit
  io.print(s + " " + Int.to_string(Ports.port))
end

fn deferred() -> Unit
  defer note("done")
  with Ports.port = 9090
  io.print(Ports.port)
end

fn branching(on: Bool) -> Unit
  with Ports.port = 7070
  if on then
    io.print(Ports.port)
  else
    io.print("off")
  end
end

fn main()
  io.print(Ports.port)
  deferred()
  branching(True)
  branching(False)
  io.print(Ports.port)
end
`, "8080\n9090\ndone 8080\n7070\noff\n8080\n", "boot", "deferred", "branching", "main")
}

// A boot answering an anonymous record retains and runs; with no name, its
// fields are read by no one.
func TestIRAppBoot_AnonymousBootRuns(t *testing.T) {
	verifyDeferProgram(t, `import std::io

fn boot() -> Result<{port: Int, execution: Context}, String>
  Ok({port: 8080, execution: Context.root()})
end

fn main()
  io.print("ran")
end
`, "ran\n", "boot", "main")
}

// A dispatch whose erased receiver is the call's final, impure operand (an
// app-field read) retains and runs: the receiver is a temporary like any
// operand, so nothing forces it first.
func TestIRAppBoot_AFinalErasedReceiverRuns(t *testing.T) {
	verifyDeferProgram(t, `import std::io

interface Named
  fn name(named: self) -> String
end

struct Plain
    label: String
end
impl Named for Plain
  fn name(plain: Plain) -> String
      plain.label
  end
end

struct App
  named: Named
  context: Context
end

fn boot() -> Result<App, String>
  Ok(App{named: Plain{label: "p"}, context: Context.root()})
end

fn show() -> String
    Named.name(App.named)
end

fn main()
  io.print(show())
end
`, "p\n", "boot", "show", "main")
}

// `with` statements in the shapes that are not a named function's own
// straight body: a lambda's body, a body that returns early after the
// `with`, a branch, and a closure made after a `with` line and called after
// the function returned. Each retains and runs; the closure reads the field in
// force where it is called.
func TestIRAppBoot_WithStatementShapesRun(t *testing.T) {
	verifyDeferProgram(t, `import std::io

struct App
  label: String
  context: Context
end

fn boot() -> Result<App, String>
  Ok(App{label: "base", context: Context.root()})
end

fn label() -> String
    App.label
end

fn early(on: Bool) -> String
  with App.label = "early"
  if on then
    return label()
  end
  "late ${label()}"
end

fn branch(on: Bool) -> String
  if on then
    with App.label = "branch"
    io.print(label())
  end
  label()
end

fn mapped() -> List<String>
  [1, 2]
    |> Iter.map(fn i ->
      with App.label = "lambda ${i}"
      label()
    end)
    |> Iter.to_list()
end

fn maker() -> () -> String
  with App.label = "made"
  fn -> label()
end

fn main()
  io.print(early(True))
  io.print(early(False))
  io.print(branch(True))
  io.print(mapped())
  f = maker()
  io.print(f())
  with App.label = "main"
  io.print(f())
  io.print(label())
end
`, "early\nlate early\nbranch\nbase\n[lambda 1, lambda 2]\nbase\nmain\nmain\n",
		"boot", "label", "early", "branch", "mapped", "maker", "main")
}

// A `with` in an inline `Iter.loop` callback holds for the iteration that
// ran it: each jump back to the head, and the `break` out, restores the scope
// read at the head. A stateless loop's `with` ends with its iteration too.
func TestIRAppBoot_WithInALoopEndsWithTheIteration(t *testing.T) {
	verifyDeferProgram(t, `import std::io

struct App
  label: String
  context: Context
end

fn boot() -> Result<App, String>
  Ok(App{label: "base", context: Context.root()})
end

fn label() -> String
    App.label
end

fn counted() -> Int
  Iter.loop(0, fn n ->
    io.print("start ${label()}")
    with App.label = "iter ${n}"
    io.print(label())
    if n == 2 then
      break n
    end
    n + 1
  end)
end

fn single() -> String
  Iter.loop(fn ->
    with App.label = "single"
    io.print(label())
    break
  end)
  label()
end

fn main()
  io.print(counted())
  io.print(label())
  io.print(single())
  io.print(label())
end
`, "start base\niter 0\nstart base\niter 1\nstart base\niter 2\n2\nbase\nsingle\nbase\nbase\n",
		"boot", "label", "counted", "single", "main")
}

// A `with` in a binding `else` fallback inside an inline `Iter.loop` holds
// to the end of the else block, including its fallback value, and ends
// there: the statements after the binding read the field as it was.
func TestIRAppBoot_WithInALoopBindingElseFallback(t *testing.T) {
	verifyDeferProgram(t, `import std::io

struct App
  label: String
  context: Context
end

fn boot(_startup: Startup) -> Result<App, String>
  Ok(App{label: "base", context: Context.root()})
end

fn label() -> String
    App.label
end

fn pick(n: Int) -> Maybe<String>
  if n == 1 then None else Some("v${n}")
end

fn walk() -> Int
  Iter.loop(0, fn n ->
    Some(v) = pick(n) else
      with App.label = "fallback ${n}"
      io.print(label())
      label()
    end
    io.print("${v} ${label()}")
    if n == 2 then
      break n
    end
    n + 1
  end)
end

fn main()
  io.print(walk())
  io.print(label())
end
`, "v0 base\nfallback 1\nfallback 1 base\nv2 base\n2\nbase\n", "boot", "label", "walk", "main")
}

// A block statement that is not the last statement of its body and returns
// early from a nested `if`: the return leaves the activation with the block's
// override in force, and runs its deferred call; the normal exit runs the
// deferred call and restores the field, and the body's later statements run.
// The deferred call reads the app as it was at the `defer`. The same holds
// for a bare `return` in a Unit function and for a lambda's `return`.
func TestIRAppBoot_BlockStatementReturnsEarly(t *testing.T) {
	verifyDeferProgram(t, `import std::io

struct App
  label: String
  context: Context
end

fn boot(_startup: Startup) -> Result<App, String>
  Ok(App{label: "base", context: Context.root()})
end

fn label() -> String
    App.label
end

fn close(name: String) -> Unit
  io.print("close ${name} ${label()}")
end

fn first(on: Bool) -> String
  begin
    defer close("first")
    with App.label = "inner"
    io.print("in block ${label()}")
    if on then
      return "early ${label()}"
    end
    io.print("block end")
  end
  "late ${label()}"
end

fn quiet(on: Bool) -> Unit
  begin
    if on then
      io.print("quiet early")
      return
    end
    with App.label = "quiet"
  end
  io.print("quiet late ${label()}")
end

fn mapped() -> List<String>
  [1, 2]
    |> Iter.map(fn i ->
      begin
        with App.label = "lambda"
        if i == 1 then
          return "one ${label()}"
        end
      end
      "other ${label()}"
    end)
    |> Iter.to_list()
end

fn main()
  io.print(first(True))
  io.print(first(False))
  quiet(True)
  quiet(False)
  io.print(mapped())
  io.print(label())
end
`, "in block inner\nclose first base\nearly inner\nin block inner\nblock end\nclose first base\nlate base\n"+
		"quiet early\nquiet late base\n[one lambda, other base]\nbase\n",
		"boot", "label", "close", "first", "quiet", "mapped", "main")
}

// A block statement in a lambda's body is its own scope: a `with` in it ends
// with the block, and the lambda's later statements read the field as it was.
func TestIRAppBoot_WithInALambdasBlockStatement(t *testing.T) {
	verifyDeferProgram(t, `import std::io

struct App
  label: String
  context: Context
end

fn boot() -> Result<App, String>
  Ok(App{label: "base", context: Context.root()})
end

fn label() -> String
    App.label
end

fn both() -> List<String>
  ["a"]
    |> Iter.map(fn tag ->
      begin
        with App.label = "inner ${tag}"
        io.print(label())
      end
      label()
    end)
    |> Iter.to_list()
end

fn main()
  io.print(both())
end
`, "inner a\n[base]\n", "boot", "label", "both", "main")
}
