package analysis

import "testing"

const resultWideningDecls = `interface Named {
    fn name(value: self): String
}

struct Pt {
    x: Int
}

impl Named for Pt {
    fn name(p: Pt): String {
        "pt"
    }
}

`

// A function's declared result is its tail's expected type, as an annotation
// is a binding's: the embedded and implementor coercion reaches inside a
// tuple, a list and a type argument at a result as it does at an annotated
// binding (§8 Embedded Types: "a binding, an argument, a result"). The tail
// was compared without it, so `(1, Pt{x: 5})` was rejected as a
// `(Int, Named)` result though `t: (Int, Named) = (1, Pt{x: 5})` was not.
func TestResultWidening_TailWidensAsABindingDoes(t *testing.T) {
	src := resultWideningDecls + `fn pair(): (Int, Named) {
    (1, Pt{x: 5})
}

fn early(n: Int): (Int, Named) {
    if n > 0 {
        return (n, Pt{x: n})
    }
    (0, Pt{x: 0})
}

fn gen<A>(a: A): (A, Named) {
    (a, Pt{x: 5})
}

fn bound(t: (Int, Named)): (Int, Named) {
    t
}

fn binding(): (Int, Named) {
    t: (Int, Named) = (1, Pt{x: 5})
    t
}
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// The function's own type parameters stay rigid at the result: a tail of
// another type is still a mismatch. A `return` of a T where an Int is
// declared was accepted, since unification takes a type parameter on the
// value's side as a hole.
func TestResultWidening_OwnTypeParametersStayRigid(t *testing.T) {
	cases := map[string]string{
		"tail":            "fn wrap<T>(x: T): List<T> {\n    x\n}\n",
		"return":          "fn wrap<T>(x: T): List<T> {\n    return x\n}\n",
		"tuple":           "fn pair<A, B>(a: A, b: B): (A, B) {\n    (b, a)\n}\n",
		"concrete result": "fn f<T>(x: T): Int {\n    return x\n}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSource(resultWideningDecls + src)
			expectError(t, errs, "return type mismatch")
		})
	}
}
