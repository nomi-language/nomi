package analysis_test

import (
	"strings"
	"testing"
)

// A file a facade imports is not an item of the facade. Selecting it
// (`import facade.{leaf}`) is an error at the name that says to import the file
// directly, whether or not the facade uses it, and whatever alias it takes.
func TestSelectingAnotherFilesImportIsRejected(t *testing.T) {
	leaf := "pub fn make_box(n: Int): Box { Box{value: n} }\n" +
		"pub struct Box { value: Int }\n"
	facade := "import leaf\n\npub fn seven(): Int { leaf.make_box(7).value }\n"
	want := "`leaf` is a file that `facade` imports, not an item it exports; import `leaf` directly"
	for _, entry := range []string{
		"import facade.{leaf}\n\nfn main() {\n  _b = leaf.make_box(7)\n}\n",
		"import facade.{leaf as l}\n\nfn main() {\n  _b = l.make_box(7)\n}\n",
		"import facade.leaf\n\nfn main() {\n  _b = leaf.make_box(7)\n}\n",
	} {
		errs := buildProjectExpectingErrors(t, entry, map[string]string{
			"facade": facade,
			"leaf":   leaf,
		})
		var got []string
		found := false
		for _, e := range errs {
			got = append(got, diagText(e))
			if diagText(e) == want {
				found = true
				if e.Line != 1 || e.Col != len("import facade.")+1 && e.Col != len("import facade.{")+1 {
					t.Errorf("%s: the error is at %d:%d, want the selected name on line 1", entry, e.Line, e.Col)
				}
			}
		}
		if len(got) == 0 {
			t.Fatalf("the front end ADMITS selecting a file another file imports:\n%s", entry)
		}
		if !found {
			t.Errorf("want error %q for\n%s\ngot:\n  %s", want, entry, strings.Join(got, "\n  "))
		}
	}
}

// Importing the file directly is the replacement, and checks clean.
func TestImportingTheFileDirectlyIsAccepted(t *testing.T) {
	errs := buildProjectExpectingErrors(t,
		"import leaf\n\nfn main() {\n  _n = leaf.make_box(7).value\n}\n",
		map[string]string{"leaf": "pub fn make_box(n: Int): Box { Box{value: n} }\npub struct Box { value: Int }\n"})
	for _, e := range errs {
		t.Errorf("unexpected error: %s", diagText(e))
	}
}
