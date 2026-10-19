package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// checkEveryOrder checks a project whose texts name the file holding the
// declaration under test "%s", once as "a" and once as "z", so it is
// built before the files that read it (user files are built in the order
// of their module keys) and after them. Every file must check clean both
// times. It answers the two programs by that file's name.
func checkEveryOrder(t *testing.T, texts map[string]string) map[string]*analysis.CheckedProgram {
	t.Helper()
	out := map[string]*analysis.CheckedProgram{}
	for _, name := range []string{"a", "z"} {
		named := map[string]string{}
		for key, text := range texts {
			named[strings.ReplaceAll(key, "%s", name)] = strings.ReplaceAll(text, "%s", name)
		}
		p := newManager().CheckProgram("/nomi-order/"+name, "main", named)
		out[name] = p
		for _, key := range p.Keys() {
			for _, e := range p.Errors[key] {
				t.Errorf("declared in %s.nomi: %s.nomi:%d: %s", name, key, e.Line, e.Message)
			}
		}
	}
	return out
}

// An impl whose header names a generic interface declared in a file built
// later read the interface before its type parameters and functions were
// built: `impl Holds<Int> for Box` was "Holds expects 0 type argument(s),
// got 1", and `impl Holds for Bag` recorded no type argument for the
// unifier to bind Holds's T from.
func TestDeclarationOrder_ImplHeaderOfAnInterfaceFromAnotherFile(t *testing.T) {
	programs := checkEveryOrder(t, map[string]string{
		"%s": `pub interface Holds<T> {
    fn held(value: self) -> T
}

pub interface Combine<Rhs, Out> {
    fn combine(lhs: self, rhs: Rhs) -> Out
}
`,
		"impls": `import %s.{Holds, Combine}

pub struct Box {
    n: Int
}

pub struct Bag {
    s: String
}

impl Holds<Int> for Box {
    fn held(value: Box) -> Int {
        value.n
    }
}

impl Holds for Bag {
    fn held(value: Bag) -> String {
        value.s
    }
}

impl Combine<Int, Box> for Box {
    fn combine(lhs: Box, rhs: Int) -> Box {
        #Box{n: lhs.n + rhs}
    }
}

impl Add<Int, Box> for Box {
    fn add(lhs: Box, rhs: Int) -> Box {
        #Box{n: lhs.n + rhs}
    }
}
`,
		"main": `import {
    impls.{Box, Bag}
    %s.{Holds, Combine}
}

fn first<H>(h: H) -> Int where H: Holds<Int> {
    H.held(h)
}

fn second<H>(h: H) -> String where H: Holds<String> {
    H.held(h)
}

fn main() {
    b = #Box{n: 2}
    dbg first(b)
    dbg second(#Bag{s: "x"}) + "y"
    dbg Combine.combine(b, 3).n
    dbg (b + 4).n
}
`,
	})
	for name, p := range programs {
		impls := p.Files["impls"]
		if impls == nil {
			t.Fatalf("declared in %s.nomi: impls.nomi not reached", name)
		}
		for _, c := range []struct{ recv, iface, args string }{
			{"Box", "Holds", "Int"},
			{"Bag", "Holds", "String"},
			{"Box", "Combine", "Int, Box"},
			{"Box", "Add", "Int, Box"},
		} {
			info := impls.ImplTypeArgs[c.recv][c.iface]
			if info == nil {
				t.Errorf("declared in %s.nomi: impl %s for %s recorded no type arguments", name, c.iface, c.recv)
				continue
			}
			var args []string
			for _, a := range info.Args {
				args = append(args, a.String())
			}
			if got := strings.Join(args, ", "); got != c.args {
				t.Errorf("declared in %s.nomi: impl %s for %s has type arguments %s, want %s", name, c.iface, c.recv, got, c.args)
			}
		}
	}
}
