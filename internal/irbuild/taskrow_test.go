package irbuild

// The checked mirror for `taskFuncs`' parameter names and arity.
//
// `taskPlacement` reads the names off the row rather than off the stdlib index,
// because inside std/tasks' own gen the index has no entry for
// `Task.spawn_all`. A row is a copy of the declaration, so this reads
// std/tasks.nomi through `std.Load()` and fails if any row's names or arity
// stop matching it. A std rename is then a red test here rather than a named
// argument that lands on the wrong slot.

import (
	"sort"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// taskDeclParams is `impl Task`'s declarations, name -> parameter names.
func taskDeclParams(t *testing.T) map[string][]string {
	t.Helper()
	lib := std.Load()
	nodes := lib.Nodes["tasks"]
	if len(nodes) == 0 {
		t.Fatal("std/tasks has no nodes, so the mirror has nothing to compare")
	}
	out := map[string][]string{}
	for _, n := range nodes {
		impl, isImpl := n.(*ast.ImplBlock)
		if !isImpl {
			continue
		}
		// The inherent `impl Task<T>` block only. std/tasks also carries
		// `impl Debug for ...` blocks, whose `inspect` is not a Task function
		// and whose parameter names have nothing to do with this table.
		if impl.Interface != nil {
			continue
		}
		for _, item := range impl.Items {
			switch m := item.(type) {
			case *ast.ExternFunc:
				out[m.Name] = paramNamesOf(m.Params)
			case *ast.FuncDef:
				out[m.Name] = paramNamesOf(m.Params)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no inherent `impl Task` items found; the extraction is wrong, not the table")
	}
	return out
}

func paramNamesOf(params []ast.Param) []string {
	out := make([]string, 0, len(params))
	for _, p := range params {
		out = append(out, p.Name)
	}
	return out
}

// TestTask_RowShapeMatchesStdSource is the mirror. See the header. It also
// lists the `Task` functions std declares that the table does not mention.
func TestTask_RowShapeMatchesStdSource(t *testing.T) {
	decl := taskDeclParams(t)

	methods := make([]string, 0, len(taskFuncs))
	for name := range taskFuncs {
		methods = append(methods, name)
	}
	sort.Strings(methods)

	for _, method := range methods {
		fn := taskFuncs[method]
		want, declared := decl[method]
		if !declared {
			t.Errorf("taskFuncs has a row for Task.%s, which std/tasks does not declare. "+
				"Either the row is dead or std renamed it; a row for a declaration that "+
				"does not exist can never be reached.", method)
			continue
		}
		if len(fn.names) != len(want) {
			t.Errorf("Task.%s: row names %v, std declares %v; the arity disagrees, so a "+
				"named argument would be placed against the wrong parameter list",
				method, fn.names, want)
			continue
		}
		for i := range want {
			if fn.names[i] != want[i] {
				t.Errorf("Task.%s parameter %d: row says %q, std/tasks.nomi says %q. "+
					"argSlotPlan places a named argument by NAME, so this is a wrong SLOT "+
					"rather than a compile error.", method, i, fn.names[i], want[i])
			}
		}
		if fn.args != len(want) {
			t.Errorf("Task.%s: row arity %d, std declares %d parameter(s)",
				method, fn.args, len(want))
		}
		if len(fn.tags) != len(want) {
			t.Errorf("Task.%s: row has %d placement tag(s) for %d parameter(s); "+
				"argSlotPlan reads len(params) as the arity, so a short tag slice "+
				"silently shrinks the slot space", method, len(fn.tags), len(want))
		}
	}

	// The other direction: a `Task` function std declares and this table omits
	// is not an error (a call to it refuses `unlowered Task function`), but it
	// is listed, because the table is also the list of what the builder lowers.
	missing := []string{}
	for name := range decl {
		if _, known := taskFuncs[name]; !known {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Logf("std/tasks declares %v with no taskFuncs row; each refuses "+
			"`unlowered Task function`", missing)
	}
}

// TestTask_NoTaskParameterCarriesADefault checks the ground `taskCallArgs`
// stands on when it refuses a non-contiguous slot plan: no `Task.` row has a
// defaulted parameter, so a hole is a call no arity spells. The mirror above
// compares names and arity only, so std adding `max_running: Int = 8` would
// leave it green. A defaulted parameter would make a call that omits it legal
// and this builder would refuse it; the cost is a refusal rather than a wrong
// answer, so this is a test and not a panic.
func TestTask_NoTaskParameterCarriesADefault(t *testing.T) {
	lib := std.Load()
	nodes := lib.Nodes["tasks"]
	if len(nodes) == 0 {
		t.Fatal("std/tasks has no nodes, so the check has nothing to read")
	}
	checked := 0
	for _, n := range nodes {
		impl, isImpl := n.(*ast.ImplBlock)
		if !isImpl || impl.Interface != nil {
			continue
		}
		for _, item := range impl.Items {
			var name string
			var params []ast.Param
			switch m := item.(type) {
			case *ast.ExternFunc:
				name, params = m.Name, m.Params
			case *ast.FuncDef:
				name, params = m.Name, m.Params
			default:
				continue
			}
			if _, known := taskFuncs[name]; !known {
				continue
			}
			checked++
			for i, p := range params {
				if p.Default == nil {
					continue
				}
				t.Errorf("Task.%s parameter %d (%s) carries a default. "+
					"taskCallArgs refuses a non-contiguous slot plan on the ground that "+
					"no Task parameter has one, so a call omitting it by name is "+
					"refused where it should fill the default. Fill holes there "+
					"or restate the ground.", name, i, p.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("checked no Task declarations, so this passed vacuously")
	}
}
