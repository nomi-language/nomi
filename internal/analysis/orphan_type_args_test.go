package analysis_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// The orphan rule admits an impl of a foreign interface for a foreign
// receiver when a type the module declares heads a type argument the
// interface's functions take (spec §13, *Orphan rule*). These tests build
// projects of several modules on disk: app, the entry's module, and the
// libraries its go.mod replaces with sibling directories.

// buildModules writes files (paths relative to a fresh root, the entry at
// app/main.nomi) and builds and checks the entry's program: the build's
// errors, the program's, and the entry's body errors.
func buildModules(t *testing.T, files map[string]string) []analysis.TypeError {
	t.Helper()
	root := t.TempDir()
	for rel, text := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry, _ := parser.ParseWithRecovery(lexer.Lex(files["app/main.nomi"]))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		data, err := os.ReadFile(filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi")
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		return nodes, nil
	}
	lib := std.Load()
	fa := analysis.BuildProject(entry, lib.Primitives, lib.Modules, lib.Files, filepath.Join(root, "app"), loader)
	if fa == nil {
		t.Fatal("no analysis")
	}
	errs := entryErrors(fa)
	return append(errs, analysis.CheckTypes(fa, entry)...)
}

// appModule is the entry module's manifest and go.mod, requiring the
// libraries named.
func appModule(main string, libs ...string) map[string]string {
	gomod := "module app\n\ngo 1.27.0\n"
	for _, l := range libs {
		gomod += "\nrequire " + l + " v0.0.0\n\nreplace " + l + " => ../" + l + "\n"
	}
	return map[string]string{
		"app/main.nomi": main,
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"app/go.mod":    gomod,
	}
}

// library adds a library module of one file to files.
func library(files map[string]string, name, file, text string) map[string]string {
	files[name+"/"+file+".nomi"] = text
	files[name+"/nomi.toml"] = "[module]\nname = \"" + name + "\"\n"
	files[name+"/go.mod"] = "module " + name + "\n\ngo 1.27.0\n"
	return files
}

const fxRate = `pub struct Rate {
    per_unit: Decimal
}

impl Multiply<Rate, Decimal> for Decimal {
    fn multiply(lhs: Decimal, rhs: Rate) -> Decimal {
        lhs * rhs.per_unit
    }
}

impl Add<Rate, Decimal> for Decimal {
    fn add(lhs: Decimal, rhs: Rate) -> Decimal {
        lhs + rhs.per_unit
    }
}

impl Contains<Rate> for String {
    fn contains(c: String, x: Rate) -> Bool {
        String.contains(c, Decimal.to_string(x.per_unit))
    }
}
`

const sharesCount = `pub struct Shares {
    count: Int
}

impl Multiply<Shares, Decimal> for Decimal {
    fn multiply(lhs: Decimal, rhs: Shares) -> Decimal {
        lhs * Int.to_decimal(rhs.count)
    }
}
`

// Two libraries each implement std's Multiply for std's Decimal at their
// own type, one of them Add and Contains too, and the entry uses them by
// operator, qualified call and bound, beside std's own Decimal impls.
func TestLocalTypeArgument_TwoLibrariesImplementOneStdInterfaceForOneStdType(t *testing.T) {
	files := appModule(`import {
    fx/rate.Rate
    shares/count.Shares
}

fn scale<T>(x: T, r: Rate) -> T where T: Multiply<Rate, T> {
    x * r
}

fn main() {
    r = #Rate{per_unit: 4d}
    _a: Decimal = 2.5d * r + #Rate{per_unit: 1d}
    _b: Decimal = 2.5d * #Shares{count: 3}
    _c: Decimal = 2.5d * 2.5d
    _d: Decimal = Decimal.multiply(1d, r)
    _e: Decimal = scale(1d, r)
    _f: Bool = String.contains("x4", r) and Contains.contains("x", r) and String.contains("ab", "a")
}
`, "fx", "shares")
	library(files, "fx", "rate", fxRate)
	library(files, "shares", "count", sharesCount)
	expectSelectionClean(t, buildModules(t, files))
}

// The spec's own example: a Contains impl for String at a type the module
// declares, in a program that is one module.
func TestLocalTypeArgument_ContainsForStringAtALocalTag(t *testing.T) {
	expectSelectionClean(t, buildModules(t, appModule(`pub struct Tag {
    name: String
}

impl Contains<Tag> for String {
    fn contains(c: String, x: Tag) -> Bool {
        c == x.name
    }
}

fn main() {
    _ok = String.contains("a", #Tag{name: "a"})
}
`)))
}

// Each condition the rule sets, broken once. Every case must be rejected
// with its own reason: a case the checker starts to accept fails.
func TestLocalTypeArgument_Rejections(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "the local type stands only in a result",
			files: appModule(`struct Rate {
    per_unit: Decimal
}

impl Multiply<Decimal, Rate> for Decimal {
    fn multiply(lhs: Decimal, rhs: Decimal) -> Rate {
        #Rate{per_unit: lhs * rhs}
    }
}

fn main() {
}
`),
			want: []string{"orphan impl: `Multiply`", "`Rate` stands in `Multiply`'s type parameter `Out`, which only results name"},
		},
		{
			name: "the receiver is a bare type parameter",
			files: appModule(`struct Rate {
    per_unit: Decimal
}

impl<T> Multiply<Rate, T> for T {
    fn multiply(lhs: T, rhs: Rate) -> T {
        dbg rhs
        lhs
    }
}

fn main() {
}
`),
			want: []string{"orphan impl: `Multiply`", "for the type parameter `T`", "its receiver is the bare type parameter `T`"},
		},
		{
			name: "an earlier type argument is a bare type parameter",
			files: library(appModule(`import ops/combine.Combine

struct Rate {
    n: Int
}

impl Combine<T, Rate> for List<T> {
    fn combine(x: List<T>, a: T, b: Rate) -> Int {
        dbg x
        dbg a
        b.n
    }
}

fn main() {
}
`, "ops"), "ops", "combine", `pub interface Combine<A, B> {
    fn combine(x: self, a: A, b: B) -> Int
}
`),
			want: []string{"orphan impl: `Combine`", "`Combine`'s type parameter `A`, before `Rate`, is the bare type parameter `T`"},
		},
		{
			name: "the impl is generic in the local position",
			files: appModule(`struct Tagged<T> {
    v: T
}

impl Contains<Tagged<T>> for List<T> {
    fn contains(c: List<T>, x: Tagged<T>) -> Bool {
        dbg c
        dbg x
        False
    }
}

fn main() {
}
`),
			want: []string{"orphan impl: `Contains`", "the impl is generic in `Contains`'s type parameter `T` (`Tagged<T>`)"},
		},
		{
			name: "the local type is nested in a std type",
			files: appModule(`struct Rate {
    per_unit: Decimal
}

impl Multiply<List<Rate>, Decimal> for Decimal {
    fn multiply(lhs: Decimal, rhs: List<Rate>) -> Decimal {
        dbg rhs
        lhs
    }
}

fn main() {
}
`),
			want: []string{"orphan impl: `Multiply`", "`Rate` is declared in this module but does not head the type argument `List<Rate>`"},
		},
		{
			name: "std's generic impl covers the instantiation",
			files: appModule(`struct Rate {
    per_unit: Decimal
}

impl Contains<Rate> for List<Rate> {
    fn contains(c: List<Rate>, x: Rate) -> Bool {
        dbg c
        x.per_unit > 0d
    }
}

fn main() {
}
`),
			want: []string{"overlapping impls: std's `Contains<T>` for `List` is generic in its type arguments, so it covers `Contains<Rate>`, which this impl implements"},
		},
		{
			name: "a typed literal's handler",
			files: appModule(`import {
    std/calendar.{Date, Error}
    std/literals.{Fragment, Literal}
}

struct Rate {
    per_unit: Decimal
}

impl Literal<Rate, Result<Date, Error>> for Date {
    fn from_fragments(fragments: List<Fragment<Rate>>) -> Result<Date, Error> {
        dbg fragments
        Date.parse("2024-01-01")
    }
}

fn main() {
}
`),
			want: []string{"orphan impl: `Literal`", "a typed literal's handler is chosen by its type alone"},
		},
		{
			name: "a type another module declares",
			files: library(appModule(`import fx/rate.Rate

impl Add<Rate, Rate> for Decimal {
    fn add(lhs: Decimal, rhs: Rate) -> Rate {
        #Rate{per_unit: lhs + rhs.per_unit}
    }
}

fn main() {
}
`, "fx"), "fx", "rate", fxRate),
			want: []string{"orphan impl: `Add` (declared in module \"std\") for `Decimal` (declared in module \"std\") cannot be implemented in module \"app\"",
				"the interface, the implementing type, or a type heading a type argument"},
		},
		{
			name: "no type argument at all",
			files: appModule(`impl Display for Int {
    fn to_string(n: Int) -> String {
        dbg n
        "int"
    }
}

fn main() {
}
`),
			want: []string{"orphan impl: `Display` (declared in module \"std\") for `Int` (declared in module \"std\")"},
		},
		{
			name: "a std type argument",
			files: appModule(`impl Contains<Int> for String {
    fn contains(c: String, x: Int) -> Bool {
        String.contains(c, Int.to_string(x))
    }
}

fn main() {
}
`),
			want: []string{"orphan impl: `Contains` (declared in module \"std\") for `String` (declared in module \"std\")"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectRejected(t, buildModules(t, c.files), c.want...)
		})
	}
}

// A library's result-only impl is an orphan impl of the library's module,
// reported in the program that uses it.
func TestLocalTypeArgument_AnOrphanInALibrary(t *testing.T) {
	files := library(appModule(`import fx/rate.Rate

fn main() {
    _r = #Rate{per_unit: 1d}
}
`, "fx"), "fx", "rate", `pub struct Rate {
    per_unit: Decimal
}

impl Multiply<Decimal, Rate> for Decimal {
    fn multiply(lhs: Decimal, rhs: Decimal) -> Rate {
        #Rate{per_unit: lhs * rhs}
    }
}
`)
	expectRejected(t, buildModules(t, files), "orphan impl: `Multiply`", "cannot be implemented in module \"fx\"", "which only results name")
}
