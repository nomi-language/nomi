package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// A default that leaves an argument of its own declaration to a default,
// directly or through other defaults, would expand forever, and is an
// error at the default that closes the cycle (default_type_args.go). Each
// file uses the type too, which once overflowed the stack.
func TestDefaultTypeArgs_EndlessDefaultsAreRefused(t *testing.T) {
	const use = "\nfn f(b: Box) -> Box {\n    b\n}\n\nfn main() {\n    _ = f\n}\n"
	const self = "`Box`'s default for `T` names `Box` itself, which would never end; write the argument where `Box` is used"
	cases := []struct {
		name string
		src  string
		line int
		want string
	}{
		{"direct", "struct Box<T = Box> {\n    item: Maybe<T>\n}\n", 1, self},
		{"nested in a generic", "struct Box<T = List<Box>> {\n    item: Maybe<T>\n}\n", 1, self},
		{"inside another type's written argument", "struct Wrap<W> {\n    w: W\n}\n\nstruct Box<T = Wrap<Box>> {\n    item: Maybe<T>\n}\n", 5, self},
		{"through another of its parameters", "struct Box<A = Int, T = Box<Int>> {\n    a: A\n    item: Maybe<T>\n}\n", 1, self},
		{"through another type, declared first", "struct Box<T = Other> {\n    item: Maybe<T>\n}\n\nstruct Other<U = Box> {\n    item: Maybe<U>\n}\n", 5,
			"`Other`'s default for `U` names `Box`, whose default names `Other` again, which would never end; write the argument where `Box` is used"},
		{"through another type, declared after", "struct Other<U = Box> {\n    item: Maybe<U>\n}\n\nstruct Box<T = Other> {\n    item: Maybe<T>\n}\n", 5,
			"`Box`'s default for `T` names `Other`, whose default names `Box` again, which would never end; write the argument where `Other` is used"},
		{"through an enum and a distinct type", "enum Box<T = Ids> {\n    One(T)\n    Empty\n}\n\ntype Ids<U = Box> List<U>\n", 6,
			"`Ids`'s default for `U` names `Box`, whose default names `Ids` again, which would never end; write the argument where `Box` is used"},
	}
	for _, c := range cases {
		got := checkOnceProject(t, map[string]string{"main.nomi": c.src + use}, []string{""})
		found := false
		for _, e := range got["main.nomi"] {
			if e.Message == c.want && e.Line == c.line {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %q at line %d, got:\n%s", c.name, c.want, c.line, strings.Join(messages(got["main.nomi"]), "\n"))
		}
	}
}

// A cycle through two files is refused whichever file is built first.
func TestDefaultTypeArgs_EndlessDefaultsAcrossFiles(t *testing.T) {
	files := map[string]string{
		"boxes.nomi": "import other.Other\n\npub struct Box<T = Other> {\n    item: Maybe<T>\n}\n",
		"other.nomi": "import boxes.Box\n\npub struct Other<U = Box> {\n    item: Maybe<U>\n}\n",
	}
	for _, first := range []string{"Box", "Other"} {
		tmp := t.TempDir()
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(tmp, name), []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
		}
		module := map[string]string{"Box": "boxes", "Other": "other"}[first]
		entry := "import " + module + "." + first + "\n\nfn f(b: " + first + ") -> " + first + " {\n    b\n}\n\nfn main() {\n    _ = f\n}\n"
		entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(entry))
		loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
			data, err := os.ReadFile(filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi")
			if err != nil {
				return nil, err
			}
			nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
			return nodes, nil
		}
		lib := loadStd()
		fa, siblings, siblingNodes := analysis.BuildProjectWithCache(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
		all := append(fa.TypeErrors, analysis.CheckTypes(fa, entryNodes)...)
		for key, sib := range siblings {
			all = append(all, sib.TypeErrors...)
			all = append(all, analysis.CheckTypes(sib, siblingNodes[key])...)
		}
		n := 0
		for _, m := range messages(all) {
			if strings.Contains(m, "which would never end") {
				n++
			}
			// The name a default uses is a use of its import.
			if strings.Contains(m, "is unused") {
				t.Errorf("entry using %s: %s", first, m)
			}
		}
		if n == 0 {
			t.Errorf("entry using %s: want the endless default refused, got:\n%s", first, strings.Join(messages(all), "\n"))
		}
	}
}

// A default that writes every argument of its own declaration takes no
// default there, so it ends: `Box<Int>` is an ordinary type. So does one
// whose use of itself leaves out only a parameter whose default ends.
func TestDefaultTypeArgs_SelfReferenceWithEveryArgumentIsAccepted(t *testing.T) {
	cases := []string{
		"struct Box<T = List<Box<Int>>> {\n    item: Maybe<T>\n}\n",
		"struct Box<A = Box<Int>, B = Int> {\n    a: Maybe<A>\n    b: B\n}\n",
		"struct Box<T = Other<Int>> {\n    item: Maybe<T>\n}\n\nstruct Other<U = Box> {\n    item: Maybe<U>\n}\n",
	}
	for _, src := range cases {
		src += "\nfn f(b: Box) -> Box {\n    b\n}\n\nfn main() {\n    _ = f\n}\n"
		got := checkOnceProject(t, map[string]string{"main.nomi": src}, []string{""})
		if msgs := messages(got["main.nomi"]); len(msgs) > 0 {
			t.Errorf("unexpected errors for\n%s\n%s", src, strings.Join(msgs, "\n"))
		}
	}
}
