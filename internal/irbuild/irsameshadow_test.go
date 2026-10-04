package irbuild

import "testing"

func TestIRSameOwnerShadow_LexicalCallablesRunOnTheVM(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Number { value: Int }
impl Number {
 fn read(n: Number): Int { n.value }
 fn local(n: Number): Int { read = |v: Number| v.value + 10; read(n) }
 fn different(n: Number): String {
  read = |v: Int| "local ${v}"
  read(n.value)
 }
 fn piped(n: Number): String {
  read = |v: Int| "pipe ${v}"
  n.value |> read()
 }
 fn parameter(n: Number, read: (Int) -> String): String { read(n.value) }
 fn sibling(n: Number): Int { read(n) + 1 }
}
fn main() {
 n = Number{value: 3}
 io.print(Number.local(n))
 io.print(Number.different(n))
 io.print(Number.piped(n))
 io.print(Number.parameter(n, |v: Int| "parameter ${v}"))
 io.print(Number.sibling(n))
}
`, "13\nlocal 3\npipe 3\nparameter 3\n4\n")
}

func TestIRSameOwnerShadow_InheritedDefaultLocal(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
interface Read {
 fn read(n: self): Int
 fn local(n: self): String {
  _ = n
  read = |v: Int| "default ${v}"
  7 |> read()
 }
}
struct Number { value: Int }
impl Read for Number { fn read(n: Number): Int { n.value } }
fn main() { io.print(Read.local(Number{value: 3})) }
`, "default 7\n")
}

func TestIRSameOwnerShadow_FileFunctionPrecedesSibling(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn read(n: Int): String { "file ${n}" }
struct Number { value: Int }
impl Number {
 fn read(n: Number): Int { n.value }
 fn use(n: Number): String { read(n.value) }
}
fn main() { io.print(Number.use(Number{value: 3})) }
`, "file 3\n")
}

func TestIRSameOwnerShadow_DefaultExportPreservesFileFunction(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn read(n: Int): String { "file ${n}" }
interface Read {
 fn read(n: self): Int { _ = n; 9 }
 fn use(n: self): String { _ = n; read(3) }
}
struct Number { value: Int }
impl Read for Number {}
fn main() { io.print(Read.use(Number{value: 1})) }
`, "file 3\n")
}
