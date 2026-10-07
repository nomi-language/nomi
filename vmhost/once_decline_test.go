package vmhost

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// A `once` whose initializer the builder declines blocks every body that
// reads it. The diagnostic names the `once`, at the construct its
// initializer stopped at or at its declaration, and carries the builder's
// reason; it does not blame the reading function as a whole.
//
// Each reproducer uses gapBlockLocalTypeEscapesItsBlock (a block whose value
// has a type declared inside the block), which still declines. If that gap
// is fixed, pick another construct that declines inside a `once`.
func TestUnsupported_NamesTheOnceItsReaderDeclinedFor(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	cases := []struct {
		name, src string
		// at is the "main.nomi:line:col: message" the diagnostic starts
		// with.
		at, reason string
	}{
		{
			// The once has a type the builder represents, and its
			// initializer declines: the reader lowers and is withdrawn.
			name: "initializer declines",
			src: `import std/io

once w: Int = {
    x = {
        struct Loc {
            n: Int
        }
        Loc{n: 1}
    }
    x.n
}

fn main() {
    io.print("${w}")
}
`,
			at:     "main.nomi:4:5: this `once w` is not supported yet, so `fn main` cannot run",
			reason: "[main] reads `once w`, whose initializer did not lower; [once w] a branch or block value kind with no IR type",
		},
		{
			// The once's own type is one the builder cannot represent, so
			// its initializer is never attempted and the read declines.
			name: "type not representable",
			src: `import std/io

once w = {
    struct Loc {
        n: Int
    }
    Loc{n: 1}
}

fn main() {
    io.inspect(w)
}
`,
			at:     "main.nomi:3:6: this `once w` is not supported yet, so `fn main` cannot run",
			reason: "[main] a read of `once w`, which cannot lower: once binding without a determinable type",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := checkLowers(t.TempDir(), tc.src)
			if !o.accepted {
				t.Fatalf("the front end rejects this reproducer, so it says nothing about lowering:\n%s", o.rejection)
			}
			if len(o.unplaced) > 0 || len(o.declines) != 1 {
				t.Fatalf("want one placed decline:\n%s", o.describe(tc.src))
			}
			if !strings.HasPrefix(o.declines[0], tc.at) {
				t.Errorf("decline:\n%s\nwant it to start %q", o.declines[0], tc.at)
			}
			if !strings.Contains(o.reasons[0], tc.reason) {
				t.Errorf("reason %q, want it to contain %q", o.reasons[0], tc.reason)
			}
		})
	}
}

// A declaration-level decline at a top-level `fn` names its body; only a
// `fn` declared inside a body is a nested one.
func TestUnsupported_OnlyANestedFnIsCalledNested(t *testing.T) {
	src := `fn main() {
    fn helper(): Int {
        1
    }
    helper()
}

impl Int {
    fn twice(n: Int): Int {
        n * 2
    }
}
`
	nodes, errs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	mod := &irbuild.Module{Path: "main.nomi", Nodes: nodes}
	want := map[string]bool{"main": false, "helper": true, "twice": false}
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			if fd, ok := n.(*ast.FuncDef); ok {
				nested, known := want[fd.Name]
				if !known {
					return true
				}
				delete(want, fd.Name)
				if got := nestedFunc(mod, fd); got != nested {
					t.Errorf("nestedFunc(%s) = %v, want %v", fd.Name, got, nested)
				}
			}
			return true
		})
	}
	if len(want) > 0 {
		t.Errorf("functions not found: %v", want)
	}
}
