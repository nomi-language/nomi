package irbuild

import "testing"

// `Bool.True` and `Bool.False` are Bool's variants written as any enum's
// are, `Enum.Variant`, and lower as the Bool constants the bare `True` and
// `False` are: as a value, through the module (`bool.Bool.False`), with and
// without an expected type, as an operand, and as a pattern.
func TestBoolVariant_QualifiedVariantsAreTheConstants(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::bool
    std::io
end

fn name(b: Bool) -> String
    case b
        Bool.True       -> "yes"
        bool.Bool.False -> "no"
    end
end

fn main()
    t = Bool.True
    f: Bool = bool.Bool.False
    io.print(t)
    io.print(f)
    io.print(name(Bool.False))
    io.print(name(bool.Bool.True))
    io.print([Bool.True, False])
    io.print(Bool.True == True)
    io.print(if Bool.False then 1 else 2)
end
`, "True\nFalse\nno\nyes\n[True, False]\nTrue\n2\n")
}
