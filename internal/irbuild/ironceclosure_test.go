package irbuild

import "testing"

func TestIROnceClosure_ImportedResultInference(t *testing.T) {
	verifyLambdaProgram(t, `import { std/io config }
fn main() {
  read = || config.tag
  io.print("created")
  io.print(read())
}
`, "created\nservice\n", map[string]string{
		"config.nomi": `pub once tag: String = "service"`,
	})
}

func TestIROnceClosure_SelectiveAliasAndExplicitReturn(t *testing.T) {
	verifyLambdaProgram(t, `import { std/io config.{tag as label} }
fn main() {
  read = || { return label }
  io.print(read())
}
`, "service\n", map[string]string{
		"config.nomi": `pub once tag: String = "service"`,
	})
}
