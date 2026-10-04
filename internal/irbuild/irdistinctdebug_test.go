package irbuild

import "testing"

func TestIRDistinctDebug_CompleteProgram(t *testing.T) {
	verifyLambdaProgram(t, `type Id Int
fn main(): Int {
 id = Id(42)
 dbg id
 raw = Int(id)
 dbg raw
}
`, "dbg line 4: id = Id(42)\ndbg line 6: raw = 42\n")
}

func TestIRDistinctDebug_UserOverride(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
type Id Int
impl Debug for Id {
 fn inspect(value: Id): String { _ = value; io.print("inspect"); "custom" }
}
fn make(): Id { io.print("make"); Id(7) }
fn main(): Id {
 dbg make()
}
`, "make\ninspect\ndbg line 8: make() = custom\n")
}

func TestIRDistinctDebug_ScalarLeavesAndTransparency(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
type Name String
type Ratio Float
type Enabled Bool
fn main(): Enabled {
 name = dbg Name("a\"b")
 io.print(String(name))
 dbg Ratio(1.5)
 dbg Enabled(True)
}
`, "dbg line 6: Name(\"a\\\"b\") = Name(\"a\\\"b\")\na\"b\ndbg line 8: Ratio(1.5) = Ratio(1.5)\ndbg line 9: Enabled(True) = Enabled(True)\n")
}

func TestIRDistinctDebug_OpaqueOverride(t *testing.T) {
	verifyLambdaProgram(t, `opaque type Secret Int
impl Debug for Secret {
 fn inspect(value: Secret): String { _ = value; "redacted" }
}
fn main(): Secret { dbg Secret(7) }
`, "dbg line 5: Secret(7) = redacted\n")
}
