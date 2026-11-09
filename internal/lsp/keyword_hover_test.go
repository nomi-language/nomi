package lsp

import "testing"

const keywordHoverDecls = `import std::io
import std::json.{ToJson}

interface Shape {
  fn area(s: self) -> Int

  fn doubled(s: self) -> Int where self: Display {
    area(s) * 2
  }
}

struct Sq {
  side: Int
}

impl Shape for Sq {
  fn area(s: Sq) -> Int {
    s.side * s.side
  }
}

derive ToJson for Sq

type Id Int

`

// A keyword answers with its construct, and a word that means different
// things in different places answers for the place it stands in.
func TestHover_KeywordsFollowTheirContext(t *testing.T) {
	runShapeHoverCases(t, keywordHoverDecls, []shapeHoverCase{
		{"as in an import", "import std::assertions.AssertionFailure ▮as Failure\n\nfn f() -> Result<Unit, Failure> {\n  Ok(())\n}\n",
			[]string{"import path::file as name", "Binds the imported file or item under another name", "/modules-and-imports/#import-syntax"}, []string{"Pattern as name"}},
		{"as in a pattern", "fn f(r: Result<Int, String>) -> Int {\n  case r {\n    Ok(n) ▮as whole -> n\n    Err(_) -> 0\n  }\n}\n",
			[]string{"```nomi\nPattern as name\n```", "binds the whole value it matched", "/pattern-matching/#naming-the-whole-match-with-as"}, []string{"import"}},
		{"case as an expression", "fn f(n: Int) -> Int {\n  ▮case n {\n    1 -> 2\n    _ -> 3\n  }\n}\n",
			[]string{"case: Int -> Int", "/pattern-matching/#binding-patterns-and-guards"}, []string{"A pipe stage"}},
		{"case as a pipe stage", "fn f(n: Int) -> Int {\n  n\n  |> ▮case {\n    1 -> 2\n    _ -> 3\n  }\n}\n",
			[]string{"A pipe stage: matches the piped value", "/pipes/#other-stages"}, nil},
		{"if as a pipe stage", "fn f(b: Bool) -> Int {\n  b |> ▮if { 1 } else { 2 }\n}\n",
			[]string{"A pipe stage: branches on the piped value", "/pipes/#other-stages"}, nil},
		{"dbg as an expression", "fn main() {\n  ▮dbg 1 + 2\n}\n",
			[]string{"```nomi\ndbg expr\n```", "/spec/expressions/#dbg"}, []string{"pipe stage"}},
		{"dbg as a pipe stage", "fn main() {\n  [1] |> Iter.count() |> ▮dbg\n}\n",
			[]string{"```nomi\nvalue |> dbg\n```", "passes the value on"}, nil},
		{"defer", "fn note(n: Int) {\n  io.print(n)\n}\n\nfn f(n: Int) -> Result<Int, String> {\n  ▮defer note(n)\n  Ok(n)\n}\n",
			[]string{"```nomi\ndefer call(args)\n```", "however it ends"}, []string{"defer on error"}},
		{"defer of the error-only form", "fn note(n: Int) {\n  io.print(n)\n}\n\nfn f(n: Int) -> Result<Int, String> {\n  ▮defer on error note(n)\n  Ok(n)\n}\n",
			[]string{"```nomi\ndefer on error call(args)\n```", "left through an error"}, nil},
		{"on of the error-only form", "fn note(n: Int) {\n  io.print(n)\n}\n\nfn f(n: Int) -> Result<Int, String> {\n  defer ▮on error note(n)\n  Ok(n)\n}\n",
			[]string{"```nomi\ndefer on error call(args)\n```"}, nil},
		{"error of the error-only form", "fn note(n: Int) {\n  io.print(n)\n}\n\nfn f(n: Int) -> Result<Int, String> {\n  defer on ▮error note(n)\n  Ok(n)\n}\n",
			[]string{"```nomi\ndefer on error call(args)\n```"}, nil},
		{"else of an if", "fn f(b: Bool) -> Int {\n  if b { 1 } ▮else { 2 }\n}\n",
			[]string{"The branch that runs when the `if` condition is `False`"}, []string{"binding's pattern"}},
		{"else of a binding", "fn f(m: Maybe<Int>) -> Int {\n  Some(n) = m ▮else { return 0 }\n  n\n}\n",
			[]string{"```nomi\nPattern = value else {", "does not match the binding's pattern", "/pattern-matching/#binding-with-else"}, nil},
		{"fn of a declaration answers with the function", "▮fn twice(n: Int) -> Int {\n  n * 2\n}\n",
			[]string{"```nomi\nfn twice(n: Int) -> Int\n```\n\n---\n\nDeclares a function.", "/functions-and-lambdas/#defining-a-function"}, []string{"A lambda"}},
		{"fn of an interface's required function", "fn main() {}\n\ninterface Named {\n  ▮fn name(n: self) -> String\n}\n",
			[]string{"Declares a function.", "/functions-and-lambdas/#defining-a-function"}, []string{"A lambda"}},
		{"fn of a lambda answers with the lambda", "fn main() {\n  _ = Iter.map([1], ▮fn n -> n + 1)\n}\n",
			[]string{"```nomi\nfn n: Int -> Int\n```", "Fills `Iter.map`'s", "/functions-and-lambdas/#lambdas"}, []string{"Declares a function"}},
		{"for in an impl", "impl Display ▮for Id {\n  fn to_string(value: Id) -> String {\n    \"id\"\n  }\n}\n",
			[]string{"impl Interface for Type", "Names the type the `impl` implements"}, []string{"derive"}},
		{"for in a derive", "struct Pt {\n  x: Int\n}\n\nderive ToJson ▮for Pt\n",
			[]string{"derive Interface for Type", "Names the type the `derive` writes"}, nil},
		{"or between Bools", "fn f(a: Bool, b: Bool) -> Bool {\n  a ▮or b\n}\n",
			[]string{"Bool or Bool -> Bool", "/spec/expressions/#logical-operators"}, []string{"alternative"}},
		{"or between patterns", "fn f(n: Int) -> Int {\n  case n {\n    1 ▮or 2 -> 0\n    _ -> 1\n  }\n}\n",
			[]string{"```nomi\nPattern or Pattern\n```", "either alternative"}, []string{"Bool or Bool"}},
		{"in tests membership", "fn f(n: Int) -> Bool {\n  n ▮in [1, 2]\n}\n",
			[]string{"Int in List<Int> -> Bool", "/spec/expressions/#membership-contains"}, nil},
		{"self in an interface", "fn main() {}\n\ninterface Named {\n  fn name(n: ▮self) -> String\n}\n",
			[]string{"In `interface Named`, it stands for each type that implements `Named`"}, nil},
		{"self in an import", "import std::assertions.{▮self, AssertionFailure}\n\nfn f() -> Result<Unit, AssertionFailure> {\n  _ = assertions.fail(\"x\")\n  Ok(())\n}\n",
			[]string{"Binds the file itself beside the listed items"}, []string{"implements"}},
		{"where on a function", "fn biggest<T>(a: T, b: T) -> T ▮where T: Comparable {\n  a\n}\n",
			[]string{"Requires type parameters to implement interfaces"}, []string{"default"}},
		{"where self on a default", "fn main() {}\n\ninterface Loud {\n  fn word(s: self) -> String\n\n  fn shout(s: self) -> String ▮where self: Display {\n    \"${s}!\"\n  }\n}\n",
			[]string{"On an interface's default, names another interface", "/interfaces-and-dispatch/#defaults-that-need-another-interface"}, nil},
		{"with in a derive", "struct Pt {\n  x: Int\n}\n\nderive ToJson for Pt ▮with ToJson.Options{}\n",
			[]string{"Passes options to the derive"}, []string{"application field"}},
		{"type of a distinct type", "▮type Code Int\n",
			[]string{"```nomi\ntype Name Inner\n```", "Declares a distinct type"}, nil},
		{"type of a host type", "host ▮type Handle\n",
			[]string{"```nomi\nhost type Name\n```", "implemented outside Nomi"}, []string{"distinct"}},
		{"as in a gopkg", "gopkg \"strings\" ▮as strs\n\nfn upper(s: String) -> String go strs.ToUpper\n",
			[]string{"Names the Go package handle"}, []string{"Pattern as name"}},
		{"go binding a function", "gopkg \"strings\" as strs\n\nfn upper(s: String) -> String ▮go strs.ToUpper\n",
			[]string{"```nomi\nfn name(params) -> Result go alias.Symbol\n```", "Binds an exported Go function"}, nil},
		{"go binding a type", "gopkg \"strings\" as strs\n\nopaque type B ▮go strs.Builder\n",
			[]string{"```nomi\nopaque type Name go alias.Type\n```", "Binds a Go type"}, nil},
		{"type of a Go type binding", "gopkg \"strings\" as strs\n\nopaque ▮type B go strs.Builder\n",
			[]string{"Binds a Go type to a Nomi name"}, []string{"distinct"}},
	})
}

// Some keywords' hovers name what their place holds: the type `return`
// returns, the callback `break` and `continue` leave, and what `pub`
// publishes.
func TestHover_KeywordsNameWhatTheyAct(t *testing.T) {
	runShapeHoverCases(t, keywordHoverDecls, []shapeHoverCase{
		{"return names its function's result", "fn parse(text: String) -> Result<Int, String> {\n  if text == \"\" { ▮return Err(\"empty\") }\n  Ok(1)\n}\n",
			[]string{"Here it leaves `fn parse`, which returns `Result<Int, String>`."}, nil},
		{"return in a lambda names the lambda's result", "fn main() {\n  _ = Iter.map([1], fn n -> {\n    if n > 0 { ▮return \"big\" }\n    \"small\"\n  })\n}\n",
			[]string{"Here it leaves the lambda, which returns `String`."}, []string{"fn main"}},
		{"break names the iteration", "fn main() {\n  _ = Iter.loop(0, fn n -> {\n    if n == 3 { ▮break n }\n    n + 1\n  })\n}\n",
			[]string{"Here it leaves the callback passed to `Iter.loop`."}, nil},
		{"continue names the iteration", "fn main() {\n  _ = Iter.map([1, 2], fn n -> {\n    if n == 1 { ▮continue }\n    n\n  }) |> Iter.to_list()\n}\n",
			[]string{"Here it leaves the callback passed to `Iter.map`."}, nil},
		{"break in a named callback", "fn keep(total: Int, n: Int) -> Int {\n  if n < 0 { ▮break total }\n  total + n\n}\n\nfn main() {\n  _ = Iter.reduce([1], 0, keep)\n}\n",
			[]string{"Here it leaves `fn keep`, which is passed as an iteration callback."}, nil},
		{"pub names the function", "▮pub fn parse(text: String) -> Int {\n  1\n}\n",
			[]string{"```nomi\npub fn parse\n```", "Here it publishes `fn parse`."}, nil},
		{"pub names an opaque type", "▮pub opaque type Secret Int\n",
			[]string{"```nomi\npub opaque type Secret\n```"}, nil},
		{"pub import re-exports", "▮pub import std::io.{print}\n",
			[]string{"Re-exports every item the import selects", "/modules-and-imports/#re-exports"}, nil},
	})
}

// A contextual word answers as a keyword only in its construct; used as a
// name it gets the name's hover.
func TestHover_ContextualKeywords(t *testing.T) {
	runShapeHoverCases(t, keywordHoverDecls, []shapeHoverCase{
		{"group as a name", "fn main() {\n  ▮group = 1\n  io.inspect(group)\n}\n",
			[]string{"group: Int"}, []string{"Holds tests"}},
		{"setup as a name", "fn main() {\n  setup = 1\n  io.inspect(▮setup)\n}\n",
			[]string{"setup: Int"}, []string{"Produces the value"}},
		{"derive as a parameter", "fn twice(derive: Int) -> Int {\n  2 * ▮derive\n}\n",
			[]string{"derive: Int"}, []string{"Asks the compiler"}},
		{"host as a name", "fn main() {\n  ▮host = \"localhost\"\n  io.print(host)\n}\n",
			[]string{"host: String"}, []string{"outside Nomi"}},
		{"todo as a parameter", "fn bump(todo: Int) -> Int {\n  ▮todo + 1\n}\n",
			[]string{"todo: Int"}, []string{"A placeholder"}},
		{"todo as the placeholder", "fn later() -> Int {\n  ▮todo\n}\n",
			[]string{"```nomi\ntodo\ntodo \"reason\"\n```", "A placeholder for code not written yet"}, nil},
		{"derive as a declaration", "struct Pt {\n  x: Int\n}\n\n▮derive ToJson for Pt\n",
			[]string{"```nomi\nderive Interface for Type\n```", "/interfaces-and-dispatch/#deriving-impls"}, nil},
		{"group as a group", "▮group \"sums\" {\n  test \"one\" {\n    assert 1 == 1\n  }\n}\n",
			[]string{"Groups tests under `group \"sums\"`", "/testing/#groups-and-setup"}, nil},
		{"setup in a group", "group \"sums\" {\n  ▮setup 1\n\n  test \"one\", n {\n    assert n == 1\n  }\n}\n",
			[]string{"setup: Unit -> Int", "/testing/#groups-and-setup"}, nil},
		{"subtests", "test \"rows\" {\n  ▮subtests [1, 2], n {\n    assert n > 0\n  }\n}\n",
			[]string{"subtests: List<Int> -> Unit", "/testing/#subtests"}, nil},
		{"subtests in a group", "group \"rows\" {\n  ▮subtests [1, 2], n {\n    test \"positive\" {\n      assert n > 0\n    }\n  }\n}\n",
			[]string{"subtests rows, pattern { boot, setup and tests }", "Runs the group's boot, setup and tests once per row", "/testing/#subtests-in-a-group"}, []string{"subtests: List"}},
		{"embeds in an enum", "enum Shape2 {\n  ▮embeds Sq\n  Dot\n}\n",
			[]string{"a variant of this enum"}, nil},
		{"open in an interface", "fn main() {}\n\ninterface Named {\n  fn name(n: self) -> String\n\n  ▮open fn short(n: self) -> String {\n    name(n)\n  }\n}\n",
			[]string{"An open default of an interface"}, nil},
		{"concurrent", "fn main() {\n  x = ▮concurrent {\n    1\n  }\n  io.inspect(x)\n}\n",
			[]string{"opens a task scope", "/concurrency/"}, nil},
		{"when before a guard", "fn f(n: Int) -> Int {\n  case n {\n    k ▮when k > 1 -> k\n    _ -> 0\n  }\n}\n",
			[]string{"```nomi\nPattern when condition -> body\n```", "A guard"}, nil},
	})
}
