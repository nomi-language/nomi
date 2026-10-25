package vmhost

import (
	"path/filepath"
	"strings"
	"testing"
)

// A macro's expansion joins the declaring file: its top-level names are
// the file's, and its code is resolved in the file's scope. These tests pin
// what keeps that from going wrong silently (internal/analysis's
// macro_hygiene.go): a name an expansion declares that the file or another
// expansion declares too is an error naming both sides, and the code may
// name only what it declares, what its own imports bring and what the
// declaration writes.

// emitting is a `gen` macro whose expansion is src, Nomi text in which
// NAME stands for the declaration's name.
func emitting(src string) string {
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(src)
	quoted = strings.ReplaceAll(quoted, "NAME", "${decl.name}")
	return genMacro("        Source.new() |> Source.add(\""+quoted+"\") |> Source.expansion()", "")
}

// mappingBody is a `gen` macro whose expansion is a function with the
// declaration's name returning its body, copied with Source.mapped.
const mappingBody = `        case decl.body {
            Some(body) ->
                Source.new()
                |> Source.add("fn ${decl.name}() -> Int {\n    ")
                |> Source.mapped(body.text, body.span)
                |> Source.add("\n}\n")
                |> Source.expansion()

            None -> Declaration.error(decl, "no body")
        }`

// hygieneCase is a program of module app using ext's `gen`: what checking
// it says, or, when want is empty, what it prints.
type hygieneCase struct {
	name  string
	ext   string
	files map[string]string
	// want are texts the check must report, each with the path of the
	// file it is in before it; unwanted, texts it must not.
	want     []string
	unwanted []string
	// prints is the output of a program that checks.
	prints string
}

func (c hygieneCase) run(t *testing.T) {
	t.Helper()
	app := macroModules(t, c.ext, c.files)
	main := filepath.Join(app, "main.nomi")
	text := checkText(t, main)
	if len(c.want) == 0 {
		if text != "" {
			t.Fatalf("the program does not check:\n%s", text)
		}
		if got := runText(t, main); got != c.prints {
			t.Errorf("the program prints %q, want %q", got, c.prints)
		}
		return
	}
	if text == "" {
		t.Fatalf("the program checks; want it to report %q", c.want)
	}
	for _, w := range c.want {
		if !strings.Contains(text, filepath.Join(app, w)) {
			t.Errorf("the check does not report\n%s\nit says:\n%s", w, text)
		}
	}
	for _, u := range c.unwanted {
		if strings.Contains(text, u) {
			t.Errorf("the check reports %q:\n%s", u, text)
		}
	}
}

const mainUsing = "import {\n    ext.gen\n    std/io\n}\n\ngen! answer\n\n"

// (a) A name the expansion declares that the declaring file declares too is
// an error at the file's declaration, naming the macro declaration's line.
// The expansion that collides declares nothing, so the file's declaration
// keeps its meaning and nothing else is reported: no error inside the
// generated code, no use of the file's name reaching the macro's
// declaration.
func TestMacroHygiene_ANameTheFileDeclaresToo(t *testing.T) {
	helperAndAnswer := emitting("fn NAME() -> Int {\n    helper()\n}\n\nfn helper() -> Int {\n    2\n}\n")
	for _, c := range []hygieneCase{
		{
			name: "function",
			ext:  helperAndAnswer,
			files: map[string]string{"main.nomi": mainUsing +
				"fn helper() -> Int {\n    1\n}\n\nfn main() {\n    io.print(answer() + helper())\n}\n"},
			want: []string{
				"main.nomi:8:4: `helper` is declared here and by `gen! answer` on line 6",
				"main.nomi:8:4: help: the macro `gen` (module ext) declares `helper` beside `answer`, so rename this one",
			},
			unwanted: []string{"already defined", "a defect in the macro", "undefined"},
		},
		{
			name: "type",
			ext:  emitting("fn NAME() -> Int {\n    1\n}\n\nstruct Helper {\n    x: Int\n}\n"),
			files: map[string]string{"main.nomi": mainUsing +
				"struct Helper {\n    y: Int\n}\n\nfn main() {\n    io.print(answer() + #Helper{y: 1}.y)\n}\n"},
			want:     []string{"main.nomi:8:8: `Helper` is declared here and by `gen! answer` on line 6"},
			unwanted: []string{"already defined", "has no field", "missing field"},
		},
		{
			name: "the declared name",
			ext:  emitting("fn NAME() -> Int {\n    1\n}\n"),
			files: map[string]string{"main.nomi": mainUsing +
				"fn answer() -> Int {\n    2\n}\n\nfn main() {\n    io.print(answer())\n}\n"},
			want: []string{
				"main.nomi:8:4: `answer` is declared here and by `gen! answer` on line 6",
				"main.nomi:8:4: help: rename one of them",
			},
			unwanted: []string{"already defined"},
		},
		{
			name: "an imported name",
			ext:  emitting("fn NAME() -> Int {\n    1\n}\n\nstruct Point {\n    x: Int\n}\n"),
			files: map[string]string{
				"main.nomi":   "import {\n    ext.gen\n    shapes.Point\n    std/io\n}\n\ngen! answer\n\nfn main() {\n    io.print(answer() + #Point{x: 1}.x)\n}\n",
				"shapes.nomi": "pub struct Point {\n    x: Int\n}\n",
			},
			want:     []string{"main.nomi:3:12: `Point` is imported here and declared by `gen! answer` on line 7"},
			unwanted: []string{"already defined", "collides"},
		},
		{
			name: "renamed, it runs",
			ext:  helperAndAnswer,
			files: map[string]string{"main.nomi": mainUsing +
				"fn mine() -> Int {\n    1\n}\n\nfn main() {\n    io.print(answer() + mine())\n}\n"},
			prints: "3\n",
		},
	} {
		t.Run(c.name, c.run)
	}
}

// (b) Two declarations of one file whose expansions declare one name are an
// error at the later one, naming the earlier; the later declares nothing.
// Two files are two scopes, so expansions in two files that declare one
// name never collide, and each file's name is reached through its file.
func TestMacroHygiene_TwoExpansionsDeclareOneName(t *testing.T) {
	ext := emitting("pub fn NAME() -> Int {\n    helper()\n}\n\npub fn helper() -> Int {\n    2\n}\n")
	for _, c := range []hygieneCase{
		{
			name:  "in one file",
			ext:   ext,
			files: map[string]string{"main.nomi": "import {\n    ext.gen\n    std/io\n}\n\ngen! one\n\ngen! two\n\nfn main() {\n    io.print(one() + two())\n}\n"},
			want: []string{
				"main.nomi:8:1: `helper` is declared by `gen! two` here and by `gen! one` on line 6",
				"main.nomi:8:1: help: the macro `gen` (module ext) declares `helper` for each declaration, so two of them in one file collide: a defect in the macro, which can name it after the declaration (`two_helper`)",
			},
			unwanted: []string{"already defined", "undefined"},
		},
		{
			name:  "one name twice",
			ext:   ext,
			files: map[string]string{"main.nomi": "import {\n    ext.gen\n    std/io\n}\n\ngen! one\n\ngen! one\n\nfn main() {\n    io.print(one())\n}\n"},
			want:  []string{"main.nomi:8:1: `one` is declared by `gen! one` here and by `gen! one` on line 6"},
		},
		{
			name: "in two files",
			ext:  ext,
			files: map[string]string{
				"main.nomi": "import {\n    a\n    b\n    std/io\n}\n\nfn main() {\n    io.print(a.one() + b.two() + a.helper() + b.helper())\n}\n",
				"a.nomi":    "import ext.gen\n\npub gen! one\n",
				"b.nomi":    "import ext.gen\n\npub gen! two\n",
			},
			prints: "8\n",
		},
	} {
		t.Run(c.name, c.run)
	}
}

// (c) The expansion's code names only what it declares, what its own
// imports bring and what the declaration writes. A bare name that reaches
// the declaring file's own function, type or import instead is an error at
// the declaration, naming where the file declares it; a name nothing
// declares is the error in the generated code it always was. A local of
// the code, a type parameter, a type the declaration writes, a name the
// user wrote in the body and the macro copied, and the macro's own import
// of a file the declaring file imports too all mean what the macro meant.
func TestMacroHygiene_TheCodeNamesOnlyWhatItMayReach(t *testing.T) {
	const helperFn = "fn helper() -> Int {\n    1\n}\n\n"
	for _, c := range []hygieneCase{
		{
			name:  "the file's function",
			ext:   emitting("fn NAME() -> Int {\n    helper()\n}\n"),
			files: map[string]string{"main.nomi": mainUsing + helperFn + "fn main() {\n    io.print(answer() + helper())\n}\n"},
			want: []string{
				"main.nomi:6:1: the macro `gen` (module ext) expanded `gen! answer` to code that names `helper`, which this file declares on line 8: a macro's code names only what it declares, what its own imports bring and what the declaration writes, a defect in the macro",
				"main.nomi:6:1: help: the macro imports what its code needs at the top of the expansion (`import std/strings`) and names it through that import",
			},
		},
		{
			name: "the file's type",
			ext:  emitting("fn NAME() -> Pair {\n    #Pair{a: 1, b: 2}\n}\n"),
			files: map[string]string{"main.nomi": mainUsing +
				"struct Pair {\n    a: Int\n    b: Int\n}\n\nfn main() {\n    io.print(answer().a)\n}\n"},
			want: []string{"main.nomi:6:1: the macro `gen` (module ext) expanded `gen! answer` to code that names `Pair`, which this file declares on line 8"},
		},
		{
			name:  "the file's import",
			ext:   emitting("fn NAME() -> String {\n    strings.String.to_upper(\"x\")\n}\n"),
			files: map[string]string{"main.nomi": "import {\n    ext.gen\n    std/io\n    std/strings\n}\n\ngen! answer\n\nfn main() {\n    io.print(answer())\n    io.print(strings.String.to_upper(\"y\"))\n}\n"},
			want:  []string{"main.nomi:7:1: the macro `gen` (module ext) expanded `gen! answer` to code that names `strings`, which this file imports on line 4"},
		},
		{
			name: "another expansion's name",
			ext: genMacro(`        case decl.name {
            "one" -> Source.new() |> Source.add("fn one() -> Int {\n    1\n}\n") |> Source.expansion()
            _ -> Source.new() |> Source.add("fn ${decl.name}() -> Int {\n    one()\n}\n") |> Source.expansion()
        }`, ""),
			files: map[string]string{"main.nomi": "import {\n    ext.gen\n    std/io\n}\n\ngen! one\n\ngen! two\n\nfn main() {\n    io.print(two())\n}\n"},
			want:  []string{"main.nomi:8:1: the macro `gen` (module ext) expanded `gen! two` to code that names `one`, which `gen! one` on line 6 declares"},
		},
		{
			name:  "an import no one brings",
			ext:   emitting("fn NAME() -> String {\n    strings.String.to_upper(\"x\")\n}\n"),
			files: map[string]string{"main.nomi": mainUsing + "fn main() {\n    io.print(answer())\n}\n"},
			want:  []string{"main.nomi:6:1: the macro `gen` (module ext) expanded `gen! answer` to code with an error, a defect in the macro: "},
		},
		{
			name:   "its own import, which the file has too",
			ext:    emitting("import std/strings\n\nfn NAME() -> String {\n    strings.String.to_upper(\"x\")\n}\n"),
			files:  map[string]string{"main.nomi": "import {\n    ext.gen\n    std/io\n    std/strings\n}\n\ngen! answer\n\nfn main() {\n    io.print(answer())\n    io.print(strings.String.to_upper(\"y\"))\n}\n"},
			prints: "X\nY\n",
		},
		{
			name:   "a local named as the file's function",
			ext:    emitting("fn NAME() -> Int {\n    helper = 40\n    helper + 2\n}\n"),
			files:  map[string]string{"main.nomi": mainUsing + helperFn + "fn main() {\n    io.print(answer() + helper())\n}\n"},
			prints: "43\n",
		},
		{
			name: "a type parameter named as the file's type",
			ext:  emitting("fn NAME() -> Int {\n    pick(1, 2)\n}\n\nfn pick<Pair>(a: Pair, b: Pair) -> Pair {\n    _ = b\n    a\n}\n"),
			files: map[string]string{"main.nomi": mainUsing +
				"struct Pair {\n    a: Int\n}\n\nfn main() {\n    io.print(answer() + #Pair{a: 1}.a)\n}\n"},
			prints: "2\n",
		},
		{
			name:   "a type the declaration writes",
			ext:    emitting("fn NAME() -> Maybe<Pair> {\n    Some(#Pair{a: 5})\n}\n"),
			files:  map[string]string{"main.nomi": "import {\n    ext.gen\n    std/io\n}\n\ngen! answer -> Maybe<Pair>\n\nstruct Pair {\n    a: Int\n}\n\nfn main() {\n    io.print(Maybe.with_default(answer() |> Maybe.map(fn p -> p.a), 0))\n}\n"},
			prints: "5\n",
		},
		{
			name:   "a name the body writes",
			ext:    genMacro(mappingBody, ""),
			files:  map[string]string{"main.nomi": "import {\n    ext.gen\n    std/io\n}\n\ngen! answer `helper() + 1`\n\n" + helperFn + "fn main() {\n    io.print(answer())\n}\n"},
			prints: "2\n",
		},
	} {
		t.Run(c.name, c.run)
	}
}

// (d) An expansion's `pub` names are the declaring file's public names,
// which another file reaches through it; its private names are the
// file's private names, which the declaring file may use and no other
// file reaches.
func TestMacroHygiene_GeneratedNamesAndOtherFiles(t *testing.T) {
	ext := emitting("pub fn NAME() -> Int {\n    secret()\n}\n\npub fn shared() -> Int {\n    3\n}\n\nfn secret() -> Int {\n    4\n}\n")
	for _, c := range []hygieneCase{
		{
			name: "pub names",
			ext:  ext,
			files: map[string]string{
				"main.nomi":  "import {\n    other\n    std/io\n}\n\nfn main() {\n    io.print(other.answer() + other.shared() + other.uses())\n}\n",
				"other.nomi": "import ext.gen\n\npub gen! answer\n\npub fn uses() -> Int {\n    secret() * 10\n}\n",
			},
			prints: "47\n",
		},
		{
			name: "a private name",
			ext:  ext,
			files: map[string]string{
				"main.nomi":  "import {\n    other\n    std/io\n}\n\nfn main() {\n    io.print(other.secret())\n}\n",
				"other.nomi": "import ext.gen\n\npub gen! answer\n",
			},
			want: []string{"main.nomi:7:20: file 'other' has no member 'secret'"},
		},
	} {
		t.Run(c.name, c.run)
	}
}
