package analysis_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/std"
)

// TestDocumentManager_LambdaPassedAsCommandBuilderIsTyped opens a file whose
// lambdas build an enum variant and are passed where `(Int) -> Command` is
// expected. The document path once typed each lambda's result as Unit and
// reported "expected (Int) -> Command, got (Int) -> Unit".
func TestDocumentManager_LambdaPassedAsCommandBuilderIsTyped(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "lambda_return"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "commands.nomi")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	dm.SetWorkspaceRoot(root)

	doc := dm.Open("file://"+path, string(content))
	if doc.Analysis == nil {
		t.Fatal("expected analysis")
	}
	for _, e := range doc.Analysis.TypeErrors {
		t.Errorf("unexpected diagnostic at %d:%d: %s", e.Line, e.Col, e.Message)
	}
}
