package format

import "testing"

// A `// comment` after a type-body member stays on the member's line, and
// formatting it again changes nothing. roundTrip checks both.

func TestFormat_EnumVariant_TrailingComment(t *testing.T) {
	for name, src := range map[string]string{
		"bare":           "enum Op\n    ToJson // note\n    FromJson\nend\n",
		"bare last":      "enum Op\n    ToJson\n    FromJson // note\nend\n",
		"type payload":   "enum Op\n    Add Int        // a\n    Mul (Int, Int) // m\nend\n",
		"generic":        "enum Op<T>\n    A Maybe<T> // a\n    B (T) -> T // b\nend\n",
		"struct payload": "enum Shape\n    Rect {w: Int, h: Int} // r\n    Circle {r: Int}       // c\nend\n",
		"embeds":         "enum E\n    A        // a\n    embeds P // p\nend\n",
		"doc comment":    "enum Op\n    /// doc\n    A // a\n    B\nend\n",
		"own-line after": "enum Op\n    A // a\n\n    // above B\n    B\n    // tail\nend\n",
	} {
		t.Run(name, func(t *testing.T) { roundTrip(t, src) })
	}
}

// Canonicalizing a variant's payload keeps its comment beside it.
func TestFormat_EnumVariant_TrailingComment_Migrates(t *testing.T) {
	migrates(t,
		"enum Op\n    Add(Int) // a\n    A\n    B // b\nend\n",
		"enum Op\n    Add Int // a\n    A\n    B // b\nend\n")
}

// In a comma-separated field list the comment follows the comma, and a
// trailing comment forces the broken layout.
func TestFormat_BracedFields_TrailingComment(t *testing.T) {
	roundTrip(t, "enum Shape\n    Rect {\n        w: Int, // w\n        h: Int, // h\n        // end\n    } // r\nend\n")
	roundTrip(t, "fn f(p: {\n    x: Int, // x\n    y: Int, // y\n}) -> Int\n    1\nend\n")
	migrates(t,
		"enum Shape\n    Rect {\n        w: Int // w\n        h: Int // h\n    }\nend\n",
		"enum Shape\n    Rect {\n        w: Int, // w\n        h: Int, // h\n    }\nend\n")
}

func TestFormat_StructField_TrailingComment(t *testing.T) {
	roundTrip(t, "struct P\n    x: Int     // x\n    y: Int = 1 // y\n    z: Int\nend\n")
}

func TestFormat_InterfaceMember_TrailingComment(t *testing.T) {
	roundTrip(t, "interface Shape\n    fn area(s: self) -> Int // a\n\n    fn twice(s: self) -> Int\n        2\n    end // t\nend\n")
}

func TestFormat_TrailingComment_OtherItemPositions(t *testing.T) {
	for name, src := range map[string]string{
		"import block": "import\n    std::math    // m\n    std::strings // s\nend\n",
		"binding":      "fn main()\n    x = 1          // x\n    y: Int = x + 1 // y\nend\n",
		"case arm":     "fn f(x: Int) -> Int\n    case x\n        1 -> 2 // one\n        _ -> 3 // other\n    end\nend\n",
		"group":        "group \"g\"\n    clock Clock.Virtual // c\n\n    boot server.boot() // b\n    setup {name: \"x\"}  // s\n\n    test \"a\", {name:}\n        assert name == \"x\" // a\n    end // end\nend\n",
	} {
		t.Run(name, func(t *testing.T) { roundTrip(t, src) })
	}
}
