package vmhost

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/rotation"
)

// The two directions of ir.BodySum, the sum a stored pass holds each
// function a case can run by (testpass.go): code that only moved sums as it
// did, and code that changed sums anew.

// bodySums are the sums of the entry module's functions that have a
// linkage name, by key, and of its test cases' bodies, by name, with each
// function's first line. A function that reads a typed literal's cell is
// left out and counted: the cell is named by the literal's position
// (irbuild's irliteralcell.go), the known gap TestSkippedCasesWouldPass
// names.
func bodySums(p *Program) (sums map[string][32]byte, lines map[string]int, literals int) {
	sums, lines = map[string][32]byte{}, map[string]int{}
	if p.entry == nil {
		return sums, lines, 0
	}
	note := func(key string, f *ir.Func) {
		if readsLiteralCell(f) {
			literals++
			return
		}
		sums[key], lines[key] = ir.BodySum(f), f.Pos().Line()
	}
	for _, f := range p.entry.Funcs() {
		if f.Sym() != nil && f.Sym().Link() != "" {
			note(ir.FuncKey(f), f)
		}
	}
	for _, c := range p.entry.Tests() {
		note("test "+c.Name(), c.Fn())
	}
	return sums, lines, literals
}

// readsLiteralCell reports whether f, or a lambda it makes, reads the cell
// of a typed literal, which irbuild names `literal <text> at <line>:<col>`.
func readsLiteralCell(f *ir.Func) bool {
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			switch n := in.(type) {
			case *ir.Ref:
				if n.Kind() == ir.RefOnce && strings.HasPrefix(n.Sym().Name(), "literal ") {
					return true
				}
			case *ir.FuncValue:
				if readsLiteralCell(n.Body()) {
					return true
				}
			}
		}
	}
	return false
}

// topLevelDecl is a line that opens a file-level declaration.
var topLevelDecl = regexp.MustCompile(`^(pub )?(fn|test|group|struct|enum|impl|interface|type|once|derive|subtype) `)

// movedVariants are src with a blank line added above every file-level
// declaration, and with every blank line above one taken away: the same
// code at other lines.
func movedVariants(src string) map[string]string {
	lines := strings.SplitAfter(src, "\n")
	var added, removed strings.Builder
	for i, line := range lines {
		if topLevelDecl.MatchString(line) {
			added.WriteString("\n")
		}
		added.WriteString(line)
		if strings.TrimSpace(line) == "" && i+1 < len(lines) && topLevelDecl.MatchString(lines[i+1]) {
			continue
		}
		removed.WriteString(line)
	}
	return map[string]string{"lines added": added.String(), "lines removed": removed.String()}
}

// TestBodySum_CodeThatOnlyMovedSumsAsItDid loads a sample of the corpus's
// files (every one under NOMI_CORPUS_WALKS=full), then each again with a
// blank line added above every file-level declaration, and with every
// blank line above one taken away, and requires every function of the file
// that has a linkage name, and every test body, to have the sum it had,
// but those that read a typed literal's cell (bodySums). It requires too
// that the edits moved functions.
func TestBodySum_CodeThatOnlyMovedSumsAsItDid(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a sample of the corpus three times; -short")
	}
	walk := rotation.CorpusWalk(t, 12)
	var files []string
	_ = filepath.WalkDir("../tests", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".nomi") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	compared, moved, loaded, literals := 0, 0, 0, 0
	for i, file := range files {
		if !walk.Takes(i) {
			continue
		}
		abs, err := filepath.Abs(file)
		if err != nil {
			t.Fatal(err)
		}
		src, err := os.ReadFile(abs)
		if err != nil {
			t.Fatal(err)
		}
		opts := []Option{WithFileSystem(DiskFilesAt(TestDir(abs)))}
		was, err := Load(abs, opts...)
		if err != nil {
			continue
		}
		loaded++
		sums, lines, lits := bodySums(was)
		literals += lits
		for variant, text := range movedVariants(string(src)) {
			if text == string(src) {
				continue
			}
			now, err := LoadFileSource(abs, text, opts...)
			if err != nil {
				t.Errorf("%s with %s does not load, though it did as it is: %v", file, variant, err)
				continue
			}
			nowSums, nowLines, _ := bodySums(now)
			for key, sum := range sums {
				got, ok := nowSums[key]
				if !ok {
					// An attached test's name holds its lines.
					continue
				}
				compared++
				if nowLines[key] != lines[key] {
					moved++
				}
				if got != sum {
					t.Errorf("%s with %s: %s at line %d (was %d) sums anew, though only its lines moved",
						file, variant, strings.TrimLeft(key, "\x00\x01"), nowLines[key], lines[key])
				}
			}
		}
	}
	t.Logf("%d files loaded, %d functions compared, %d of them moved; %d read a typed literal's cell and were not compared", loaded, compared, moved, literals)
	if moved == 0 {
		t.Fatal("no function moved, so nothing was compared")
	}
}

// bodySumBase is a program each case of TestBodySum_ChangedCodeSumsAnew
// edits in one place.
const bodySumBase = `struct Point
    x: Int
    y: Int
end

fn make(a: Int) -> Point
    Point{x: a, y: a + 1}
end

fn helper(n: Int) -> Int
    n * 2
end

fn other(n: Int) -> Int
    n * 3
end

fn calls(n: Int) -> Int
    if n > 0 then
        helper(n) + 1
    else
        0
    end
end

fn untouched(n: Int) -> Int
    n - 7
end

test "uses them"
    assert make(1).y == 2
    assert calls(2) == 5
    assert untouched(8) == 1
end
`

// TestBodySum_ChangedCodeSumsAnew edits one function of bodySumBase, or
// one type its body names, and requires the function's sum to change and
// untouched's to stay: a constant, an operator, a callee, a branch, a
// field's place in a type's layout and the signature each count.
func TestBodySum_ChangedCodeSumsAnew(t *testing.T) {
	for _, c := range []struct {
		edit, from, to, fn string
	}{
		{"a constant", "y: a + 1}", "y: a + 2}", "make"},
		{"an operator", "y: a + 1}", "y: a - 1}", "make"},
		{"a callee", "        helper(n) + 1", "        other(n) + 1", "calls"},
		{"a branch", "if n > 0 then", "if n >= 0 then", "calls"},
		{"a layout", "    x: Int\n    y: Int\n", "    y: Int\n    x: Int\n", "make"},
		{"a signature", "fn helper(n: Int) -> Int\n    n * 2", "fn helper(n: Int, k: Int = 0) -> Int\n    n * 2 + k", "helper"},
		{"a parameter's type", "fn other(n: Int) -> Int\n    n * 3", "fn other(n: Float) -> Float\n    n * 3.0", "other"},
		{"nothing", "    n - 7", "    n - 7", "untouched"},
	} {
		t.Run(c.edit, func(t *testing.T) {
			edited := strings.Replace(bodySumBase, c.from, c.to, 1)
			if c.from != c.to && edited == bodySumBase {
				t.Fatalf("the edit %q does not apply", c.from)
			}
			sumOf := func(src string) map[string][32]byte {
				p, err := LoadSource("bodysum.nomi", src)
				if err != nil {
					t.Fatalf("the program does not load: %v\n%s", err, src)
				}
				out := map[string][32]byte{}
				for _, f := range p.entry.Funcs() {
					out[f.Name()] = ir.BodySum(f)
				}
				return out
			}
			was, now := sumOf(bodySumBase), sumOf(edited)
			if was["untouched"] != now["untouched"] && c.fn != "untouched" {
				t.Errorf("untouched sums anew after %s in %s", c.edit, c.fn)
			}
			if c.from == c.to {
				// The control: no edit, no new sum.
				if was[c.fn] != now[c.fn] {
					t.Errorf("%s sums anew with nothing edited", c.fn)
				}
				return
			}
			if was[c.fn] == now[c.fn] {
				t.Errorf("%s sums as it did after %s", c.fn, c.edit)
			}
		})
	}
}
