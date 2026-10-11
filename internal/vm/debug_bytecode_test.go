package vm_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

var topLevelFn = regexp.MustCompile(`(?m)^(?:pub )?fn ([a-z_][A-Za-z0-9_]*)\(`)

// TestDebug_AMachineWithoutADebuggerCompilesNoHooks compiles every top-level
// function of the corpus's programs twice, on a plain machine and on a debug
// machine. The plain listing holds no `stmt` or `pc` hook, and the debug
// listing with its hooks taken out is the plain one, opcode for opcode and
// operand for operand, except for branch targets, which the hooks move, and
// a jump to a `ret`, which a plain machine folds into the `ret` and a debug
// machine keeps when a hook sits at its target.
func TestDebug_AMachineWithoutADebuggerCompilesNoHooks(t *testing.T) {
	root := filepath.Join("..", "..", "tests")
	compared, hooked := 0, 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".nomi") || strings.HasSuffix(path, "_test.nomi") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		names := topLevelFn.FindAllStringSubmatch(string(src), -1)
		if len(names) == 0 {
			return nil
		}
		plain, err := vmhost.Load(path)
		if err != nil {
			return nil
		}
		debug, err := vmhost.Load(path)
		if err != nil {
			t.Fatalf("%s loads once and not twice: %v", path, err)
		}
		debug.SetDebugger(vm.NewDebugger())
		for _, n := range names {
			want, err := plain.Disassemble(n[1])
			if err != nil {
				continue
			}
			got, err := debug.Disassemble(n[1])
			if err != nil {
				t.Fatalf("%s.%s: %v", path, n[1], err)
			}
			if strings.Contains(want, " stmt ") || strings.Contains(want, " pc ") || strings.Contains(want, " tap ") {
				t.Fatalf("%s.%s: a plain machine compiled a debugger's hook:\n%s", path, n[1], want)
			}
			if strings.Contains(got, " stmt ") {
				hooked++
			}
			if diff := sameBytecode(want, got); diff != "" {
				t.Fatalf("%s.%s: the debug listing is not the plain one plus hooks: %s\nplain:\n%s\ndebug:\n%s",
					path, n[1], diff, want, got)
			}
			compared++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if compared < 100 || hooked < compared/2 {
		t.Fatalf("compared %d functions, %d with a statement hook; the population is wrong", compared, hooked)
	}
	t.Logf("%d functions compile the same on both machines, %d with statement hooks", compared, hooked)
}

// sameBytecode compares two listings as the test above describes, answering
// the first difference or "".
func sameBytecode(plain, debug string) string {
	instrs := func(listing string, dropHooks bool) [][]string {
		var out [][]string
		for _, line := range strings.Split(listing, "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			if dropHooks && (fields[1] == "stmt" || fields[1] == "pc" || fields[1] == "tap") {
				continue
			}
			if i := strings.Index(line, ";"); i >= 0 {
				fields = append(strings.Fields(line[:i]), line[i:])
			}
			out = append(out, fields[1:])
		}
		return out
	}
	p, d := instrs(plain, false), instrs(debug, true)
	if len(p) != len(d) {
		return "a different instruction count"
	}
	for i := range p {
		a, b := p[i], d[i]
		switch {
		case a[0] == b[0] && (a[0] == "jmp" || a[0] == "br" || a[0] == "brx"):
			// Targets move; the condition register and block do not.
			if a[0] != "jmp" && (a[1] != b[1] || (a[0] == "brx" && a[len(a)-1] != b[len(b)-1])) {
				return "instruction " + strings.Join(b, " ") + " is " + strings.Join(a, " ")
			}
		case (a[0] == "ret" || a[0] == "retu") && b[0] == "jmp":
		default:
			if strings.Join(a, " ") != strings.Join(b, " ") {
				return "instruction " + strings.Join(b, " ") + " is " + strings.Join(a, " ")
			}
		}
	}
	return ""
}
