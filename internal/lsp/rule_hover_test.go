package lsp

import (
	"strings"
	"testing"
)

// ruleHoverCase is a hover whose whole text is known: src has one `▮`
// before the hovered character.
type ruleHoverCase struct {
	name, src, want string
}

func runRuleHoverCases(t *testing.T, cases []ruleHoverCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hoverText(t, "rules", tc.src); got != tc.want {
				t.Errorf("hover =\n%s\n\nwant\n%s", got, tc.want)
			}
		})
	}
}

// An `if` without `else` says what it is: a Maybe of its branch, or a
// statement, and the rule that made it one.
func TestHover_IfWithoutElseStatesItsRule(t *testing.T) {
	runRuleHoverCases(t, []ruleHoverCase{
		{"a Maybe", "fn digits(total: Int, digits: Map<String, Int>) -> Maybe<Map<String, Int>>\n  ▮if total == 0 then digits\nend\n",
			"```nomi\nif … then digits: Maybe<Map<String, Int>>\n```\n\n" +
				"An `if` without `else` is a `Maybe`: `Some(digits)` when `total == 0` holds, `None` when it doesn't.\n\n" +
				"More in the [spec](https://nomi-lang.org/spec/expressions/#ifelse)."},
		{"a Maybe from a pattern", "fn half(m: Maybe<Int>) -> Maybe<Int>\n  ▮if Some(n) = m then n / 2\nend\n",
			"```nomi\nif … then n / 2: Maybe<Int>\n```\n\n" +
				"An `if` without `else` is a `Maybe`: `Some(n / 2)` when `m` matches `Some(n)`, `None` when it doesn't.\n\n" +
				"More in the [spec](https://nomi-lang.org/spec/expressions/#ifelse)."},
		{"a chain", "fn sign(n: Int) -> Maybe<String>\n  if n > 0 then\n    \"positive\"\n  else ▮if n < 0 then\n    \"negative\"\n  end\nend\n",
			"```nomi\nif … then \"negative\": Maybe<String>\n```\n\n" +
				"An `if` chain without a final `else` is a `Maybe`: `Some` of the value of the branch that runs, `None` when no condition holds.\n\n" +
				"More in the [spec](https://nomi-lang.org/spec/expressions/#ifelse)."},
		{"a Unit statement", "import std::io\n\nfn note(debug: Bool)\n  ▮if debug then io.print(\"x\")\nend\n",
			"```nomi\nif … then io.print(\"x\")\n```\n\n" +
				"An `if` without `else` whose branch is `Unit` is a statement: it runs the branch when `debug` holds and has no value.\n\n" +
				"More in the [spec](https://nomi-lang.org/spec/expressions/#ifelse)."},
		{"a statement that leaves", "fn check(n: Int) -> Result<Int, String>\n  ▮if n < 0 then return Err(\"negative\")\n  Ok(n)\nend\n",
			"```nomi\nif … then return Err(\"negative\")\n```\n\n" +
				"An `if` without `else` whose branch leaves (`return`) is a statement with no value: the code after it runs only when `n < 0` doesn't hold.\n\n" +
				"More in the [spec](https://nomi-lang.org/spec/expressions/#ifelse)."},
	})
}

// A binding's `else` says when it runs and what it must do.
func TestHover_BindingElseStatesItsRule(t *testing.T) {
	runRuleHoverCases(t, []ruleHoverCase{
		{"one that leaves", "fn first(letters: List<String>) -> String\n  [letter, ..rest] = letters ▮else return \"\"\n  letter\nend\n",
			"```nomi\n[letter, ..rest] = letters else …\n```\n\n" +
				"Runs when `letters` doesn't match `[letter, ..rest]`. It must leave the function (`return`, `break`, `continue`, or a call that never returns), so the pattern's names are bound after it.\n\n" +
				"More in the [spec](https://nomi-lang.org/spec/patterns/#bindings-with-else)."},
		{"one that leaves where a fallback could stand", "fn f(m: Maybe<Int>) -> Int\n  Some(n) = m ▮else return 0\n  n\nend\n",
			"```nomi\nSome(n) = m else …\n```\n\n" +
				"Runs when `m` doesn't match `Some(n)`. Here it leaves the function (`return`), so the pattern's names are bound after it.\n\n" +
				"More in the [spec](https://nomi-lang.org/spec/patterns/#bindings-with-else)."},
		{"one with arms", "fn f(r: Result<Int, String>) -> Int\n  Ok(n) = r ▮else\n    Err(_) -> return 0\n  end\n  n\nend\n",
			"```nomi\nOk(n) = r else …\n```\n\n" +
				"Runs when `r` doesn't match `Ok(n)`; its arms match the value that didn't. Here it leaves the function (`return`); an `Int` in its place would stand in for the pattern's payload instead.\n\n" +
				"More in the [spec](https://nomi-lang.org/spec/patterns/#bindings-with-else)."},
		{"one with a fallback", "fn size(r: Result<(Int, Int), String>) -> Int\n  Ok((width, height)) = r ▮else (80, 24)\n  width * height\nend\n",
			"```nomi\nOk((width, height)) = r else …\n```\n\n" +
				"Runs when `r` doesn't match `Ok((width, height))`. Where it doesn't leave the function, its value, a `(Int, Int)`, stands in for the pattern's payload, and the pattern's names bind from it.\n\n" +
				"More in the [spec](https://nomi-lang.org/spec/patterns/#bindings-with-else)."},
	})
}

// The `[` or `{` of an unnamed collection literal names the collection the
// checker made it and what expected that type.
func TestHover_CollectionLiteralSaysWhyItsKind(t *testing.T) {
	const rule = "\n\nAn unnamed `[…]` builds the List, Vector or Set its position expects, and a List where nothing expects one."
	tagAll := "fn tag_all(tags: Set<String>) -> Int\n  Set.size(tags)\nend\n\n"
	runRuleHoverCases(t, []ruleHoverCase{
		{"a parameter's Set", tagAll + "fn main()\n  _ = tag_all(▮[\"a\", \"b\"])\nend\n",
			"```nomi\nSet<String>\n```\n\nA `Set<String>`, from the parameter `tags: Set<String>` of `tag_all`." + rule},
		{"a named argument's Set", tagAll + "fn main()\n  _ = tag_all(tags: ▮[\"a\"])\nend\n",
			"```nomi\nSet<String>\n```\n\nA `Set<String>`, from the parameter `tags: Set<String>` of `tag_all`." + rule},
		{"no expected type", "import std::io\n\nfn main()\n  xs = ▮[1, 2]\n  io.inspect(xs)\nend\n",
			"```nomi\nList<Int>\n```\n\nA `List<Int>` (no expected type)." + rule},
		{"an annotation's Vector", "import std::io\n\nfn main()\n  xs: Vector<Int> = ▮[1, 2]\n  io.inspect(xs)\nend\n",
			"```nomi\nVector<Int>\n```\n\nA `Vector<Int>`, from the annotation `xs: Vector<Int>`." + rule},
		{"a result type", "fn ids() -> Set<Int>\n  ▮[1, 2]\nend\n",
			"```nomi\nSet<Int>\n```\n\nA `Set<Int>`, from the result type of `ids`." + rule},
		{"a map", "import std::io\n\nfn main()\n  m = ▮{\"a\" => 1}\n  io.inspect(m)\nend\n",
			"```nomi\nMap<String, Int>\n```\n\nA `Map<String, Int>` (no expected type)."},
	})
}

// punSrc has a punned label of each kind: a struct literal's, an update's,
// a pattern's and a named argument's.
const punSrc = `struct Birth
  name: String
  on: Int
end

fn make(name: String) -> Birth
  on = 3
  Birth{name:, on:}
end

fn later(b: Birth) -> Birth
  on = 4
  {..b, on:}
end

fn day(b: Birth) -> Int
  Birth{on:} = b
  on
end

fn json(status: Int, body: String) -> String
  body
end

fn main()
  status = 200
  _ = json(status:, body: "x")
end
`

// markPun is punSrc with a `▮` before word on the line holding line.
func markPun(t *testing.T, line, word string) string {
	t.Helper()
	i := strings.Index(punSrc, line)
	if i < 0 {
		t.Fatalf("no %q", line)
	}
	j := i + strings.Index(line, word)
	return punSrc[:j] + "▮" + punSrc[j:]
}

// A punned `name:` shows both things it is.
func TestHover_PunShowsLabelAndBinding(t *testing.T) {
	runRuleHoverCases(t, []ruleHoverCase{
		{"a struct literal", markPun(t, "Birth{name:, on:}", "on:"),
			"```nomi\non: Int\n```\n\nfield `on` of `Birth`, filled from the binding `on` (line 7). `on:` is short for `on: on`."},
		{"a parameter in a struct literal", markPun(t, "Birth{name:, on:}", "name:"),
			"```nomi\nname: String\n```\n\nfield `name` of `Birth`, filled from the parameter `name` (line 6). `name:` is short for `name: name`."},
		{"an update", markPun(t, "{..b, on:}", "on:"),
			"```nomi\non: Int\n```\n\nfield `on` of `Birth`, replaced by the binding `on` (line 12). `on:` is short for `on: on`."},
		{"a pattern", markPun(t, "Birth{on:} = b", "on:"),
			"```nomi\non: Int\n```\n\nfield `on` of `Birth`, bound to a new binding `on`. `on:` is short for `on: on`."},
		{"a named argument", markPun(t, "_ = json(status:", "status:"),
			"```nomi\nstatus: Int\n```\n\nparameter `status` of `json`, from the binding `status` (line 26). `status:` is short for `status: status`."},
	})
}
