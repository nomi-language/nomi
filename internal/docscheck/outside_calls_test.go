package docscheck

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/compilerhosts"
	"github.com/nomi-language/nomi/internal/vm"
)

// outsideTableHeader opens the table in docs/tooling.md's "Capability
// lists" that names, for each std module, its functions a call to which is
// an outside call.
const outsideTableHeader = "| Module | Outside functions |"

// outsideRows is that table as the code has it: each std module that
// declares an outside crossing (vm.OutsideCrossings, and std/compiler's
// hosts), with the functions it declares, sorted. A crossing a `host`
// module declares is its parent module's, which a program imports: the
// `std::fs::host` functions are std::fs's.
func outsideRows() map[string][]string {
	keys := vm.OutsideCrossings()
	for _, name := range compilerhosts.Names() {
		if outside, _ := compilerhosts.Outside(name); outside {
			keys[name] = "compiler"
		}
	}
	rows := map[string][]string{}
	for key, std := range keys {
		module := "std::" + strings.TrimSuffix(std, "::host")
		fn := key[strings.LastIndex(key, ".")+1:]
		if !slices.Contains(rows[module], fn) {
			rows[module] = append(rows[module], fn)
		}
	}
	for _, fns := range rows {
		sort.Strings(fns)
	}
	return rows
}

// renderOutsideTable is rows as the table docs/tooling.md holds.
func renderOutsideTable(rows map[string][]string) string {
	modules := make([]string, 0, len(rows))
	for m := range rows {
		modules = append(modules, m)
	}
	sort.Strings(modules)
	var b strings.Builder
	b.WriteString(outsideTableHeader + "\n|---|---|\n")
	for _, m := range modules {
		fns := make([]string, len(rows[m]))
		for i, f := range rows[m] {
			fns[i] = "`" + f + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s |\n", m, strings.Join(fns, ", "))
	}
	return b.String()
}

// The tooling reference's table of outside calls is the one the binding
// rows, the VM's intrinsics and std/compiler's hosts give, so a function
// marked outside, or no longer outside, shows there.
func TestOutsideCallTableMatchesTheBindings(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "tooling.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, outsideTableHeader)
	if start < 0 {
		t.Fatalf("docs/tooling.md holds no table headed %q", outsideTableHeader)
	}
	end := strings.Index(text[start:], "\n\n")
	if end < 0 {
		end = len(text) - start
	}
	got := text[start:start+end] + "\n"
	want := renderOutsideTable(outsideRows())
	if got != want {
		t.Errorf("docs/tooling.md's table of outside calls is not the one the code gives; replace it with:\n\n%s", want)
	}
}
