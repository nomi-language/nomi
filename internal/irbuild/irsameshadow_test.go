package irbuild

import "testing"

func TestIRSameOwnerShadow_LexicalCallablesRunOnTheVM(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
struct Number
    value: Int
end
impl Number
 fn read(n: Number) -> Int
     n.value
 end
 fn local(n: Number) -> Int
     read = fn v: Number -> v.value + 10
     read(n)
 end
 fn different(n: Number) -> String
  read = fn v: Int -> "local ${v}"
  read(n.value)
 end
 fn piped(n: Number) -> String
  read = fn v: Int -> "pipe ${v}"
  n.value |> read()
 end
 fn parameter(n: Number, read: (Int) -> String) -> String
     read(n.value)
 end
 fn sibling(n: Number) -> Int
     read(n) + 1
 end
end
fn main()
 n = Number{value: 3}
 io.print(Number.local(n))
 io.print(Number.different(n))
 io.print(Number.piped(n))
 io.print(Number.parameter(n, fn v: Int -> "parameter ${v}"))
 io.print(Number.sibling(n))
end
`, "13\nlocal 3\npipe 3\nparameter 3\n4\n")
}

func TestIRSameOwnerShadow_InheritedDefaultLocal(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
interface Read
 fn read(n: self) -> Int
 fn local(n: self) -> String
  _ = n
  read = fn v: Int -> "default ${v}"
  7 |> read()
 end
end
struct Number
    value: Int
end
impl Read for Number
    fn read(n: Number) -> Int
        n.value
    end
end
fn main()
    io.print(Read.local(Number{value: 3}))
end
`, "default 7\n")
}

func TestIRSameOwnerShadow_FileFunctionPrecedesSibling(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn read(n: Int) -> String
    "file ${n}"
end
struct Number
    value: Int
end
impl Number
 fn read(n: Number) -> Int
     n.value
 end
 fn use(n: Number) -> String
     read(n.value)
 end
end
fn main()
    io.print(Number.use(Number{value: 3}))
end
`, "file 3\n")
}

func TestIRSameOwnerShadow_DefaultExportPreservesFileFunction(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn read(n: Int) -> String
    "file ${n}"
end
interface Read
 fn read(n: self) -> Int
     _ = n
     9
 end
 fn use(n: self) -> String
     _ = n
     read(3)
 end
end
struct Number
    value: Int
end
impl Read for Number end
fn main()
    io.print(Read.use(Number{value: 1}))
end
`, "file 3\n")
}
