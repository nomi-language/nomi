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
		{"capabilities-and-context.md:L13", `import {
  std/io
}

struct App {
  port: Int
  context: Context
}

fn boot(): App {
  App{port: 8080, context: Context.root()}
}

fn main() {
  io.print("listening on :${App.port}")
}
`, "listening on :8080\n", []string{"boot", "main"}},
		{"capabilities-and-context.md:L80", `import {
  std/io
}

interface Logger {
  fn log(logger: self, msg: String): Unit
}

type Stdout
impl Logger for Stdout {
  fn log(_logger: Stdout, msg: String): Unit {
    io.print("[log] ${msg}")
  }
}

struct PrefixedLogger { tag: String }
impl Logger for PrefixedLogger {
  fn log(logger: PrefixedLogger, msg: String): Unit {
    io.print("[${logger.tag}] ${msg}")
  }
}

struct App {
  logger: Logger
  context: Context
}

fn boot(): App {
  App{logger: Stdout, context: Context.root()}
}

fn main() {
  greet("World")
  audit_greet("Alice")
  greet("Bob")
}

fn audit_greet(name: String) {
  with App.logger = PrefixedLogger{tag: "audit"}
  greet(name)
}

fn greet(name: String) {
  Logger.log(App.logger, "hello, ${name}")
}
`, "[log] hello, World\n[audit] hello, Alice\n[log] hello, Bob\n",
			[]string{"boot", "main", "audit_greet", "greet", "Stdout.log", "PrefixedLogger.log"}},
		{"field reads in a helper", `import std/io

struct App {
  name: String
  port: Int
  context: Context
}

fn base(): Int { 8000 }

fn boot(): App {
  App{name: "api", port: base() + 80, context: Context.root()}
}

fn address(): String { "${App.name}:${App.port}" }

fn main() {
  io.print(address())
  io.print(App.port + 1)
}
`, "api:8080\n8081\n", []string{"boot", "address", "main"}},
		{"a struct-typed field", `import std/io

struct Config {
  host: String
  port: Int
}

struct App {
  config: Config
  context: Context
}

fn boot(): App {
  App{config: Config{host: "localhost", port: 5432}, context: Context.root()}
}

fn main() {
  c = App.config
  io.print("${c.host}:${c.port}")
}
`, "localhost:5432\n", []string{"boot", "main"}},
		{"a scoped write restored after the function returns", `import std/io

struct App {
  level: Int
  label: String
  context: Context
}

fn boot(): App {
  App{level: 1, label: "base", context: Context.root()}
}

fn show(): String { "${App.label}@${App.level}" }

fn louder(): String {
  with App.level = App.level + 10
  with App.label = String.to_lower("LOUD")
  io.print(App.level)
  io.print(show())
  deeper()
}

fn deeper(): String {
  with App.level = App.level * 2
  show()
}

fn main() {
  io.print(show())
  io.print(louder())
  io.print(show())
}
`, "base@1\n11\nloud@11\nloud@22\nbase@1\n", []string{"boot", "show", "louder", "deeper", "main"}},
		{"an erased field bound, then dispatched", `import std/io

interface Shape {
  fn area(shape: self): Int
}

struct Square { side: Int }
impl Shape for Square {
  fn area(square: Square): Int { square.side * square.side }
}

struct Rect { w: Int  h: Int }
impl Shape for Rect {
  fn area(rect: Rect): Int { rect.w * rect.h }
}

struct App {
  shape: Shape
  context: Context
}

fn boot(): App {
  App{shape: Square{side: 3}, context: Context.root()}
}

fn report(label: String): Unit {
  s = App.shape
  io.print("${label}: ${Shape.area(s)}")
}

fn wide(): Unit {
  with App.shape = Rect{w: 2, h: 5}
  report("rect")
}

fn main() {
  report("square")
  wide()
  report("square again")
}
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
	verifyDeferProgram(t, `import std/io

struct Ports {
  port: Int
  execution: Context
}

fn boot(): Ports {
  Ports{port: 8080, execution: Context.root()}
}

fn note(s: String): Unit {
  io.print(s + " " + Int.to_string(Ports.port))
}

fn deferred(): Unit {
  defer note("done")
  with Ports.port = 9090
  io.print(Ports.port)
}

fn branching(on: Bool): Unit {
  with Ports.port = 7070
  if on {
    io.print(Ports.port)
  } else {
    io.print("off")
  }
}

fn main() {
  io.print(Ports.port)
  deferred()
  branching(True)
  branching(False)
  io.print(Ports.port)
}
`, "8080\n9090\ndone 8080\n7070\noff\n8080\n", "boot", "deferred", "branching", "main")
}

// A boot answering an anonymous record retains and runs; with no name, its
// fields are read by no one.
func TestIRAppBoot_AnonymousBootRuns(t *testing.T) {
	verifyDeferProgram(t, `import std/io

fn boot(): {port: Int, execution: Context} {
  {port: 8080, execution: Context.root()}
}

fn main() {
  io.print("ran")
}
`, "ran\n", "boot", "main")
}

// A dispatch whose erased receiver is the call's final, impure operand (an
// app-field read) retains and runs: the receiver is a temporary like any
// operand, so nothing forces it first.
func TestIRAppBoot_AFinalErasedReceiverRuns(t *testing.T) {
	verifyDeferProgram(t, `import std/io

interface Named {
  fn name(named: self): String
}

struct Plain { label: String }
impl Named for Plain {
  fn name(plain: Plain): String { plain.label }
}

struct App {
  named: Named
  context: Context
}

fn boot(): App {
  App{named: Plain{label: "p"}, context: Context.root()}
}

fn show(): String { Named.name(App.named) }

fn main() {
  io.print(show())
}
`, "p\n", "boot", "show", "main")
}

// `with` statements in the shapes that are not a named function's own
// straight body: a lambda's body, a body that returns early after the
// `with`, a branch, and a closure made after a `with` line and called after
// the function returned. Each retains and runs; the closure reads the field in
// force where it is called.
func TestIRAppBoot_WithStatementShapesRun(t *testing.T) {
	verifyDeferProgram(t, `import std/io

struct App {
  label: String
  context: Context
}

fn boot(): App {
  App{label: "base", context: Context.root()}
}

fn label(): String { App.label }

fn early(on: Bool): String {
  with App.label = "early"
  if on {
    return label()
  }
  "late ${label()}"
}

fn branch(on: Bool): String {
  if on {
    with App.label = "branch"
    io.print(label())
  }
  label()
}

fn mapped(): List<String> {
  [1, 2]
    |> Iter.map(|i| {
      with App.label = "lambda ${i}"
      label()
    })
    |> Iter.to_list()
}

fn maker(): () -> String {
  with App.label = "made"
  || label()
}

fn main() {
  io.print(early(True))
  io.print(early(False))
  io.print(branch(True))
  io.print(mapped())
  f = maker()
  io.print(f())
  with App.label = "main"
  io.print(f())
  io.print(label())
}
`, "early\nlate early\nbranch\nbase\n[lambda 1, lambda 2]\nbase\nmain\nmain\n",
		"boot", "label", "early", "branch", "mapped", "maker", "main")
}

// A `with` in an inline `Iter.loop` callback holds for the iteration that
// ran it: each jump back to the head, and the `break` out, restores the scope
// read at the head. A stateless loop's `with` ends after its `break` value.
func TestIRAppBoot_WithInALoopEndsWithTheIteration(t *testing.T) {
	verifyDeferProgram(t, `import std/io

struct App {
  label: String
  context: Context
}

fn boot(): App {
  App{label: "base", context: Context.root()}
}

fn label(): String { App.label }

fn counted(): Int {
  Iter.loop(|n = 0| {
    io.print("start ${label()}")
    with App.label = "iter ${n}"
    io.print(label())
    if n == 2 {
      break n
    }
    n + 1
  })
}

fn single(): String {
  Iter.loop(|| {
    with App.label = "single"
    break label()
  })
}

fn main() {
  io.print(counted())
  io.print(label())
  io.print(single())
  io.print(label())
}
`, "start base\niter 0\nstart base\niter 1\nstart base\niter 2\n2\nbase\nsingle\nbase\n",
		"boot", "label", "counted", "single", "main")
}

// A `with` in a binding `else` fallback inside an inline `Iter.loop` holds
// to the end of the else block, including its fallback value, and ends
// there: the statements after the binding read the field as it was.
func TestIRAppBoot_WithInALoopBindingElseFallback(t *testing.T) {
	verifyDeferProgram(t, `import std/io

struct App {
  label: String
  context: Context
}

fn boot(_startup: Startup): App {
  App{label: "base", context: Context.root()}
}

fn label(): String { App.label }

fn pick(n: Int): Maybe<String> {
  if n == 1 { None } else { Some("v${n}") }
}

fn walk(): Int {
  Iter.loop(|n = 0| {
    Some(v) = pick(n) else {
      with App.label = "fallback ${n}"
      io.print(label())
      label()
    }
    io.print("${v} ${label()}")
    if n == 2 {
      break n
    }
    n + 1
  })
}

fn main() {
  io.print(walk())
  io.print(label())
}
`, "v0 base\nfallback 1\nfallback 1 base\nv2 base\n2\nbase\n", "boot", "label", "walk", "main")
}

// A block statement that is not the last statement of its body and returns
// early from a nested `if`: the return leaves the activation with the block's
// override in force, and runs its deferred call; the normal exit runs the
// deferred call and restores the field, and the body's later statements run.
// The deferred call reads the app as it was at the `defer`. The same holds
// for a bare `return` in a Unit function and for a lambda's `return`.
func TestIRAppBoot_BlockStatementReturnsEarly(t *testing.T) {
	verifyDeferProgram(t, `import std/io

struct App {
  label: String
  context: Context
}

fn boot(_startup: Startup): App {
  App{label: "base", context: Context.root()}
}

fn label(): String { App.label }

fn close(name: String): Unit {
  io.print("close ${name} ${label()}")
}

fn first(on: Bool): String {
  {
    defer close("first")
    with App.label = "inner"
    io.print("in block ${label()}")
    if on {
      return "early ${label()}"
    }
    io.print("block end")
  }
  "late ${label()}"
}

fn quiet(on: Bool): Unit {
  {
    if on {
      io.print("quiet early")
      return
    }
    with App.label = "quiet"
  }
  io.print("quiet late ${label()}")
}

fn mapped(): List<String> {
  [1, 2]
    |> Iter.map(|i| {
      {
        with App.label = "lambda"
        if i == 1 {
          return "one ${label()}"
        }
      }
      "other ${label()}"
    })
    |> Iter.to_list()
}

fn main() {
  io.print(first(True))
  io.print(first(False))
  quiet(True)
  quiet(False)
  io.print(mapped())
  io.print(label())
}
`, "in block inner\nclose first base\nearly inner\nin block inner\nblock end\nclose first base\nlate base\n"+
		"quiet early\nquiet late base\n[one lambda, other base]\nbase\n",
		"boot", "label", "close", "first", "quiet", "mapped", "main")
}

// A block statement in a lambda's body is its own scope: a `with` in it ends
// with the block, and the lambda's later statements read the field as it was.
func TestIRAppBoot_WithInALambdasBlockStatement(t *testing.T) {
	verifyDeferProgram(t, `import std/io

struct App {
  label: String
  context: Context
}

fn boot(): App {
  App{label: "base", context: Context.root()}
}

fn label(): String { App.label }

fn both(): List<String> {
  ["a"]
    |> Iter.map(|tag| {
      {
        with App.label = "inner ${tag}"
        io.print(label())
      }
      label()
    })
    |> Iter.to_list()
}

fn main() {
  io.print(both())
}
`, "inner a\n[base]\n", "boot", "label", "both", "main")
}
