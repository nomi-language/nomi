package lsp

import "testing"

// A std module's name answers with its import line, the first paragraph
// of the module's `//#` comments (the paragraph its reference page opens
// with) and a link to that page.
func TestHover_StdModuleNameShowsItsDocs(t *testing.T) {
	runShapeHoverCases(t, "", []shapeHoverCase{
		{"an imported module's name", "import std/uuid\n\nfn main() {\n  dbg ▮uuid.v4()\n}\n",
			[]string{"```nomi\nimport std/uuid\n```", "UUID", "More in the [reference](https://nomi-lang.org/reference/uuid/)."}, nil},
		{"the import line itself", "import std/▮uuid\n\nfn main() {\n  dbg uuid.v4()\n}\n",
			[]string{"More in the [reference](https://nomi-lang.org/reference/uuid/)."}, nil},
		{"a module imported for testing", "import std/http_testing\n\nfn main() {\n  dbg ▮http_testing.request(.Get, \"/\")\n}\n",
			[]string{"```nomi\nimport std/http_testing\n```", "Answers std/http's requests with a function instead of the network",
				"More in the [reference](https://nomi-lang.org/reference/http_testing/)."}, nil},
	})
}
