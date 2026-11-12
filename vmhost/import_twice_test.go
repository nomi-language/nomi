package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// Two imports that bind one name to two declarations are an error at the
// second, with an alias as the fix. The second import used to be reported
// only as unused.
func TestImports_TwoImportsCannotBindOneName(t *testing.T) {
	thing := "pub struct Thing\n    n: Int\nend\n"
	files := map[string]string{"a": thing, "b": thing}
	main := "import a.{Thing}\nimport b.{Thing}\n\nfn main()\n    dbg Thing{n: 1}\nend\n"
	_, err := vmhost.LoadSource("main", main, vmhost.WithVirtualFiles(files))
	for _, want := range []string{
		"'Thing' is already imported on line 1 from another file; two imports cannot bind one name",
		"import one of them under another name, as in `Thing as OtherThing`",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("LoadSource = %v; want an error holding %q", err, want)
		}
	}
}
