package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A path-only import's PathUses are the paths its scope writes below the
// name it binds, in every position a path stands, sorted and once each; an
// alias is the name, an import at the top of a block sees only its block,
// and a selective import has none.
func TestNotePathUses(t *testing.T) {
	nodes := parse(t, `import {
    views::admin
    db as store
    std::io.{print}
}

fn show(u: admin::users.Row) -> String {
    case u {
        admin::users.Row{name} -> "${admin::reports::daily.show()}${name}"
    }
}

fn main() {
    import jobs

    _ = (admin::users.index([]), store::posts.Post{id: 1}, jobs::mail.send())
}

fn other() {
    _ = jobs::queue.run()
}
`)
	block := nodes[0].(*ast.ImportBlock)
	for i, want := range []string{"reports::daily users", "posts", ""} {
		if got := strings.Join(block.Entries[i].PathUses, " "); got != want {
			t.Errorf("entry %d: PathUses %q, want %q", i, got, want)
		}
	}
	main := nodes[2].(*ast.FuncDef)
	nested := main.Body.Stmts[0].(*ast.ImportStmt)
	if got := strings.Join(nested.PathUses, " "); got != "mail" {
		t.Errorf("the block's import: PathUses %q, want %q", got, "mail")
	}
}
