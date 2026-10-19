package lsp

import "testing"

const variantHoverDecls = `import std/io

/// Where the game is.
enum Room {
  /// The cave, dark and damp.
  Cave
  Hall
  Pit(Int)
  Vault { depth: Int }
}

enum Monster {
  Goblin
}

struct Game {
  at: Room
  monsters: Map<Room, Monster>
}

fn enter(r: Room) -> Room {
  r
}

`

// A variant answers on its name and on the `.` of its shorthand with the
// variant and its enum, its payload, and its doc (or else the enum's); the
// `.` form says the enum came from the expected type. A constructor
// answers the same on its `(`.
func TestHover_VariantEverywhere(t *testing.T) {
	cave := []string{"```nomi\nRoom.Cave\n```", "The cave, dark and damp."}
	caveDot := append([]string{"*`Room` from the expected type*"}, cave...)
	// Hall has no doc of its own; the enum's stands in.
	hall := []string{"```nomi\nRoom.Hall\n```", "Where the game is."}
	hallDot := append([]string{"*`Room` from the expected type*"}, hall...)
	game := "fn main() {\n  g = #Game{\n    at: %s,\n    monsters: %{.Hall => .Goblin},\n  }\n  io.inspect(g)\n}\n"
	keyed := "fn main() {\n  g = #Game{\n    at: .Cave,\n    monsters: %{%s => .Goblin},\n  }\n  io.inspect(g)\n}\n"
	value := "fn main() {\n  r: Room = %s\n  io.inspect(r)\n}\n"
	arg := "fn main() {\n  io.inspect(enter(%s))\n}\n"
	pattern := "fn f(r: Room) -> Int {\n  case r {\n    %s -> 1\n    _ -> 0\n  }\n}\n"
	in := func(form, v string) string { return replaceOnce(form, "%s", v) }
	cases := []shapeHoverCase{
		{"the . of a value", in(value, "▮.Cave"), caveDot, nil},
		{"the name of a value", in(value, ".Ca▮ve"), caveDot, nil},
		{"the . of a value with no doc", in(value, "▮.Hall"), hallDot, nil},
		{"the name of a value with no doc", in(value, ".▮Hall"), hallDot, nil},
		{"the . of a struct field value", in(game, "▮.Cave"), caveDot, nil},
		{"the name of a struct field value", in(game, ".Cav▮e"), caveDot, nil},
		{"the . of a map key", in(keyed, "▮.Hall"), hallDot, nil},
		{"the name of a map key", in(keyed, ".Ha▮ll"), hallDot, nil},
		{"the . of an argument", in(arg, "▮.Cave"), caveDot, nil},
		{"the name of an argument", in(arg, ".▮Cave"), caveDot, nil},
		{"the . of a pattern", in(pattern, "▮.Cave"), caveDot, nil},
		{"the name of a pattern", in(pattern, ".C▮ave"), caveDot, nil},
		{"the . of a pattern with no doc", in(pattern, "▮.Hall"), hallDot, nil},
		{"the qualified name", in(value, "Room.▮Cave"), cave, []string{"from the expected type"}},
		{"the qualified name with no doc", in(value, "Room.Ha▮ll"), hall, []string{"from the expected type"}},
		{"an imported name", "import std/io\n\nenum Room {\n  /// The cave.\n  Cave\n}\n\nfn main() {\n  r = Room.Cave\n  case r {\n    ▮Cave -> io.print(\"c\")\n  }\n}\n", []string{"```nomi\nRoom.Cave\n```", "The cave."}, nil},
		{"the . of a payload constructor", in(value, "▮.Pit(2)"), []string{"```nomi\nRoom.Pit(Int)\n```", "*`Room` from the expected type*", "Where the game is."}, nil},
		{"the name of a payload constructor", in(value, ".P▮it(2)"), []string{"```nomi\nRoom.Pit(Int)\n```", "*`Room` from the expected type*"}, nil},
		{"the ( of a payload constructor", in(value, ".Pit▮(2)"), []string{"```nomi\nRoom.Pit(Int)\n```", "*`Room` from the expected type*"}, nil},
		{"the ( of a qualified constructor", in(value, "Room.Pit▮(2)"), []string{"```nomi\nRoom.Pit(Int)\n```"}, []string{"from the expected type"}},
		{"the ( of a payload pattern", in(pattern, ".Pit▮(n)"), []string{"```nomi\nRoom.Pit(Int)\n```", "*`Room` from the expected type*"}, nil},
		{"the . of a struct variant pattern", in(pattern, "#▮.Vault{depth}"), []string{"```nomi\nRoom.Vault { depth: Int }\n```", "*`Room` from the expected type*"}, nil},
		{"Some's name", "fn main() {\n  s = ▮Some(1)\n  io.inspect(s)\n}\n", []string{"```nomi\nMaybe.Some(Int) -> Maybe<Int>\n```"}, nil},
		{"Some's (", "fn main() {\n  s = Some▮(1)\n  io.inspect(s)\n}\n", []string{"```nomi\nMaybe.Some(Int) -> Maybe<Int>\n```"}, nil},
		{"a payload keeps its own hover", "fn main() {\n  s = Some(▮1)\n  io.inspect(s)\n}\n", []string{"```nomi"}, []string{"Maybe.Some"}},
	}
	runShapeHoverCases(t, variantHoverDecls, cases)
}

func replaceOnce(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}
