package analysis_test

import (
	"strings"
	"testing"
)

// Polymorphic recursion is a compile error: a generic function that reaches
// itself at a type built from its own parameter needs infinitely many
// instances. Recursion at the same parameters, at a concrete type, or with
// parameters swapped needs finitely many and is accepted.
func TestInstantiationCycle(t *testing.T) {
	const header = "struct Box<T> {\n  inner: T\n}\n\n"
	rejected := map[string]string{
		"direct, through a struct": `fn grow<T>(x: T, n: Int): Int {
  if n == 0 { 0 } else { grow(Box{inner: x}, n - 1) }
}`,
		"direct, through a tuple": `fn pair<T>(x: T, n: Int): Int {
  if n == 0 { 0 } else { pair((x, x), n - 1) }
}`,
		"mutual, through a list": `fn ping<T>(x: T, n: Int): Int {
  if n == 0 { 0 } else { pong([x], n - 1) }
}

fn pong<U>(y: U, n: Int): Int {
  ping(y, n)
}`,
	}
	for name, src := range rejected {
		errs := checkUniversalDebugProject(t, header+src+"\n\nfn main() {\n  _ = 1\n}\n")
		found := 0
		for _, e := range errs {
			if strings.Contains(e.Message, "grows on every recursive call") {
				found++
			}
		}
		if found != 1 {
			t.Errorf("%s: want one polymorphic-recursion error, got %v", name, errs)
		}
	}
	accepted := map[string]string{
		"same parameter": `fn count<T>(x: T, n: Int): Int {
  if n == 0 { 0 } else { count(x, n - 1) }
}`,
		"concrete argument": `fn once<T>(x: T, n: Int): Int {
  if n == 0 { 0 } else { once(1, n - 1) }
}`,
		"swapped parameters": `fn swap<T, U>(a: T, b: U, n: Int): Int {
  if n == 0 { 0 } else { swap(b, a, n - 1) }
}`,
		"growing but not recursive": `fn wrap<T>(x: T): Box<T> {
  Box{inner: x}
}

fn twice<T>(x: T): Box<Box<T>> {
  wrap(wrap(x))
}`,
	}
	for name, src := range accepted {
		errs := checkUniversalDebugProject(t, header+src+"\n\nfn main() {\n  _ = 1\n}\n")
		for _, e := range errs {
			if strings.Contains(e.Message, "grows") {
				t.Errorf("%s: rejected: %v", name, e)
			}
		}
	}
}
