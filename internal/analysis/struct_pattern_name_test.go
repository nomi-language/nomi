package analysis_test

import "testing"

// A struct pattern's type name must name the struct its value has. Each
// rejected program below passed the checker, and the IR builder then declined
// the binding ("a pattern binding outside the retained case tests"). The
// first is a lowering fuzz find: a test line `assert {name, age} = ...`
// mutated to `Invalissert {name, age} = ...`, which parses as a struct
// pattern of the undeclared type `Invalissert`.
func TestStructPatternName_ANameThatIsNotTheValuesStructIsRejected(t *testing.T) {
	const decls = "struct Person {\n  name: String\n  age: Int\n}\n\nstruct Pet {\n  name: String\n}\n\n"
	for _, tc := range []struct {
		name, body, want string
	}{
		{"undeclared over an anonymous struct", `Invalissert{name, age} = {name: "Grace", age: 37}`, `unknown type "Invalissert"`},
		{"undeclared over a struct", `Nope{name} = Person{name: "Lin", age: 3}`, `unknown type "Nope"`},
		{"a struct over an anonymous struct", `Person{name, age} = {name: "Ada", age: 36}`,
			"struct pattern names Person, but the value is the anonymous struct {name: String, age: Int}\n" +
				"help: drop the type name to destructure an anonymous struct"},
		{"another struct", `Pet{name} = Person{name: "Lin", age: 3}`, "struct pattern names Pet, but the value is a Person"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "fn main() {\n  " + tc.body + "\n  _ = name\n}\n"
			_, errs := checkSourceWithStdlib(src)
			expectStdlibError(t, errs, tc.want)
		})
	}
}

// The mirror: the struct's own name, an alias of it, a generic struct's
// bare name and an anonymous pattern stay accepted, in a binding and in a
// case arm.
func TestStructPatternName_TheValuesStructIsAccepted(t *testing.T) {
	const src = "struct Person {\n  name: String\n  age: Int\n}\n\n" +
		"struct Box<T> {\n  v: T\n}\n\n" +
		"typealias Human Person\n\n" +
		"fn main() {\n" +
		"  p = Person{name: \"Lin\", age: 3}\n" +
		"  Person{name} = p\n" +
		"  Human{age} = p\n" +
		"  Box{v} = Box{v: 4}\n" +
		"  {name: n2} = {name: \"Ada\"}\n" +
		"  r = case p {\n    Human{name: n3} -> n3\n  }\n" +
		"  _ = (name, age, v, n2, r)\n" +
		"}\n"
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}
