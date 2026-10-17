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
// The initializer declines at its call to `planted_decline` (plantDecline).
// A `once` whose own type the builder cannot represent, so that its
// initializer is never attempted, has no reproducer here since block-local
// types lower outside their block; add a case when one turns up.
func TestUnsupported_NamesTheOnceItsReaderDeclinedFor(t *testing.T) {
	plantDecline(t)
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

fn planted_decline(a: Int) -> Int {
    a
}

once w: Int = {
    u = planted_decline(1)
    u
}

fn main() {
    io.print("${w}")
}
`,
			at:     "main.nomi:8:9: this `once w` is not supported yet, so `fn main` cannot run",
			reason: "[main] reads `once w`, whose initializer did not lower; [once w] " + plantedReason,
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
    fn helper() -> Int {
        1
    }
    helper()
}

impl Int {
    fn twice(n: Int) -> Int {
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
