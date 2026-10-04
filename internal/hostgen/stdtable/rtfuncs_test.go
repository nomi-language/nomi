package stdtable

import (
	"github.com/nomi-language/nomi/internal/hostgen"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/stdlibbindings"
)

// TestRtFuncsAllGenerate feeds the rt-shaped stdlib host functions the VM
// calls (internal/stdlibbindings.RtFuncs) through the generator one row at a
// time, and requires every one to generate.
//
// One row at a time rather than the whole table, so a refusal reports every
// row it blocks rather than the first.
func TestRtFuncsAllGenerate(t *testing.T) {
	base := Table()
	var rows []hostgen.FuncRow
	for _, b := range stdlibbindings.RtFuncs() {
		rows = append(rows, hostgen.FuncRow{Name: b.Name, Fn: b.Fn, PanicsPropagate: b.PanicsPropagate})
	}
	adapted := 0
	byReason := map[string][]string{}
	for _, row := range rows {
		tb := base
		tb.Funcs = []hostgen.FuncRow{row}
		if _, err := hostgen.Generate(tb); err != nil {
			reason := strings.TrimPrefix(err.Error(), row.Name+": ")
			byReason[reason] = append(byReason[reason], row.Name)
			continue
		}
		adapted++
	}
	reasons := make([]string, 0, len(byReason))
	for r := range byReason {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		t.Errorf("%d refused: %s\n    %s", len(byReason[r]), r, strings.Join(byReason[r], " "))
	}
	if len(rows) == 0 {
		t.Fatal("RtFuncs is empty, so the assertion above is vacuous")
	}
	t.Logf("%d of %d rt-shaped VM hosts adapt", adapted, len(rows))
}
