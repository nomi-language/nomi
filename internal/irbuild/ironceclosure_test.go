package irbuild

import "testing"

func TestIROnceClosure_ImportedResultInference(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::io config
end
fn main()
  read = fn -> config.tag
  io.print("created")
  io.print(read())
end
`, "created\nservice\n", map[string]string{
		"config.nomi": `pub once tag: String = "service"`,
	})
}

func TestIROnceClosure_SelectiveAliasAndExplicitReturn(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::io config.{tag as label}
end
fn main()
  read = fn ->
      return label
  end
  io.print(read())
end
`, "service\n", map[string]string{
		"config.nomi": `pub once tag: String = "service"`,
	})
}
