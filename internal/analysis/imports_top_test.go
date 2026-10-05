package analysis

import (
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"strings"
	"testing"
)

// Imports must appear at the top of their enclosing block. After a
// non-import statement, no more imports are permitted. This is enforced
// at file scope (top-level decls) and inside function bodies.
//
// Rationale: mid-scope imports silently rebind names. The rule keeps
// "what does X mean here?" answerable by reading the top of the block,
// which aligns with the language's broader explicit-over-implicit posture.

func TestImports_TopOfFile_RejectsImportAfterDecl(t *testing.T) {
	src := `fn main() {}
import std/lists: List`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)
	BuildTypes(file, nodes)
	errs := CheckTypes(file, nodes)

	all := append([]TypeError{}, file.TypeErrors...)
	all = append(all, errs...)

	found := false
	for _, e := range all {
		if strings.Contains(e.Message, "import") &&
			(strings.Contains(e.Message, "top") || strings.Contains(e.Message, "before")) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an error rejecting late import at file scope, got: %v", all)
	}
}

func TestImports_FunctionBody_RejectsImportAfterStatement(t *testing.T) {
	src := `fn main() {
  x = 1
  import std/lists: List
  io.inspect(x)
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)
	BuildTypes(file, nodes)
	errs := CheckTypes(file, nodes)

	all := append([]TypeError{}, file.TypeErrors...)
	all = append(all, errs...)

	found := false
	for _, e := range all {
		if strings.Contains(e.Message, "import") &&
			(strings.Contains(e.Message, "top") || strings.Contains(e.Message, "before")) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an error rejecting late import in function body, got: %v", all)
	}
}

func TestImports_FunctionBody_TopAllowed(t *testing.T) {
	src := `fn main() {
  import std/lists: List
  Unit
}`
	_, errs := checkSource(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "import") &&
			(strings.Contains(e.Message, "top") || strings.Contains(e.Message, "before")) {
			t.Errorf("unexpected late-import error for top-of-body import: %v", e)
		}
	}
}

// The rule recurses into nested function declarations: a `fn helper`
// declared inside `fn main` must follow the same "imports at top
// of body" rule (spec §14). Walking only top-level function bodies
// would leave nested late-imports unchecked.
func TestImports_NestedFunctionBody_RejectsImportAfterStatement(t *testing.T) {
	src := `fn main() {
  fn helper(): Int {
    x = 1
    import std/lists: List
    x
  }
  io.inspect(helper())
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)
	BuildTypes(file, nodes)
	errs := CheckTypes(file, nodes)

	all := append([]TypeError{}, file.TypeErrors...)
	all = append(all, errs...)

	found := false
	for _, e := range all {
		if strings.Contains(e.Message, "import") &&
			(strings.Contains(e.Message, "top") || strings.Contains(e.Message, "before")) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an error rejecting late import in nested function body, got: %v", all)
	}
}

// Counterpart: nested fn with imports at top is allowed.
func TestImports_NestedFunctionBody_TopAllowed(t *testing.T) {
	src := `fn main() {
  fn helper(): Int {
    import std/lists: List
    3
  }
  io.inspect(helper())
}`
	_, errs := checkSource(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "import") &&
			(strings.Contains(e.Message, "top") || strings.Contains(e.Message, "before")) {
			t.Errorf("unexpected late-import error for top-of-nested-body import: %v", e)
		}
	}
}

func TestImports_TopOfFile_AllImportsFirst(t *testing.T) {
	src := `import std/lists: List
import std/maps: Map

fn main() {
  Unit
}`
	_, errs := checkSource(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "import") &&
			(strings.Contains(e.Message, "top") || strings.Contains(e.Message, "before")) {
			t.Errorf("unexpected late-import error: %v", e)
		}
	}
}

// Block-form imports count as imports for the at-top check; a regular
// ImportStmt that follows a block-form import (with no non-import in
// between) must NOT be flagged as "after another statement."
func TestImports_BlockFormCountsAsImport(t *testing.T) {
	src := `import std/lists: List
import {
  std/maps: Map
}
import std/strings: String

fn main() {
  Unit
}`
	_, errs := checkSource(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "must appear at the top") {
			t.Errorf("unexpected late-import error around block-form import: %v", e)
		}
	}
}

// Imports after a real declaration are still rejected, even if the
// declaration is followed by another import block.
func TestImports_AfterDecl_RejectedEvenForBlockForm(t *testing.T) {
	src := `import std/lists: List

struct Foo { a: Int }

import { std/maps: Map }`
	_, errs := checkSource(src)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "must appear at the top") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected late-import error for block-form import after a decl, got errors: %v", errs)
	}
}
