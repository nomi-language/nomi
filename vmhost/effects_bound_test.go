package vmhost

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// boundCases are public generic functions nothing instantiates, each of
// whose bound's functions has no one value of the type parameter to select
// an implementation by, and a private type whose impl reads a file. A
// private type's impl is no root of its own, so the read is reached only
// through the generic function's abstract body, by its call through the
// bound.
var boundCases = []struct {
	name, src, want string
}{
	{"a function that takes no value of the type", `import std::fs

pub interface Make
    fn make() -> self
end

struct Config
    text: String
end

impl Make for Config
    fn make() -> Config
        Config{text: fs.read_text("/etc/hosts") |> Result.with_default("")}
    end
end

pub fn built<T>() -> T
where T: Make
    T.make()
end
`, "genlib.nomi:13:22: this package's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
		"genlib.nomi:13:22: help: reached from `built`; `built` calls `make` through an interface, and any implementation of it counts, including `Make.make for Config`\n" +
		"genlib.nomi:13:22: help: add \"std::fs\" to effects under [effects.self] in nomi.toml, or remove the call"},
	{"a std interface's function that takes no value of the type", `import
    std::fs
    std::sql.{FromSql, Value}
end

struct Path
    text: String
end

impl FromSql for Path
    fn from_sql(_value: Value) -> Result<Path, String>
        _ = fs.read_text("/etc/hosts")
        Err("no path")
    end
end

pub fn read_value<T>(v: Value) -> Result<T, String>
where T: FromSql
    T.from_sql(v)
end
`, "genlib.nomi:12:13: this package's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
		"genlib.nomi:12:13: help: reached from `read_value`; `read_value` calls `from_sql` through an interface, and any implementation of it counts, including `FromSql.from_sql for Path`\n" +
		"genlib.nomi:12:13: help: add \"std::fs\" to effects under [effects.self] in nomi.toml, or remove the call"},
}

// A use of a type parameter's bound that no one value of the type parameter
// selects an implementation of is, in the abstract body, a call through the
// bound, which counts every implementation the program has as a dispatch
// does. Each read is refused under
// an [effects.self] that allows nothing, reached from the generic function,
// and allowed once std names std::fs.
func TestEffectRoots_ACallThroughTheBoundCountsEveryImplementation(t *testing.T) {
	for _, c := range boundCases {
		t.Run(c.name, func(t *testing.T) {
			got, ds := effectDiagnostics(t, "genlib.nomi", genericLibrary("[]", c.src))
			if got != c.want {
				t.Errorf("diagnostics:\n%s\nwant:\n%s", got, c.want)
			}
			if len(ds) != 1 || ds[0].Code != EffectNotAllowedCode {
				t.Errorf("diagnostics %v; want one %s", ds, EffectNotAllowedCode)
			}
			checkedRootsPass(t, "genlib.nomi", genericLibrary(`["std::fs"]`, c.src))
		})
	}
}

// A call through the bound reaches an implementation another file of the
// program writes, of the program's own interface and of a std interface it
// holds as one (FromSql): each file names the interface's function by one
// symbol. Each type is opaque, so its impl is no root of its own, and the
// library's other function makes no value of it.
func TestEffectRoots_ACallThroughTheBoundReachesAnotherFilesImplementation(t *testing.T) {
	impls := `import
    std::fs
    std::sql.{FromSql, Value}
    genlib::genlib.{Make}
end

pub opaque struct Token
    n: Int
end

impl Make for Token
    fn make() -> Token
        _ = fs.read_text("/etc/passwd")
        Token{n: 0}
    end
end

pub opaque struct Path
    text: String
end

impl FromSql for Path
    fn from_sql(_value: Value) -> Result<Path, String>
        _ = fs.read_text("/etc/hosts")
        Err("no path")
    end
end

pub fn zero() -> Int
    0
end
`
	cases := []struct{ name, imports, fn, want string }{
		{"the program's own interface's", "", `pub fn built<T>() -> T
where T: Make
    T.make()
end
`, "impls.nomi:13:13: this package's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
			"impls.nomi:13:13: help: reached from `built`; `built` calls `make` through an interface, and any implementation of it counts, including `Make.make for Token`\n" +
			"impls.nomi:13:13: help: add \"std::fs\" to effects under [effects.self] in nomi.toml, or remove the call"},
		{"a std interface's", "    std::sql.{FromSql, Value}\n", `pub fn read_value<T>(v: Value) -> Result<T, String>
where T: FromSql
    T.from_sql(v)
end
`, "impls.nomi:24:13: this package's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
			"impls.nomi:24:13: help: reached from `read_value`; `read_value` calls `from_sql` through an interface, and any implementation of it counts, including `FromSql.from_sql for Path`\n" +
			"impls.nomi:24:13: help: add \"std::fs\" to effects under [effects.self] in nomi.toml, or remove the call"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := genericLibrary("[]", "import\n"+c.imports+"    genlib::impls\nend\n\npub interface Make\n    fn make() -> self\nend\n\npub fn base() -> Int\n    impls.zero()\nend\n\n"+c.fn)
			files["impls.nomi"] = impls
			got, _ := effectDiagnostics(t, "genlib.nomi", files)
			if got != c.want {
				t.Errorf("diagnostics:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

// The abstract body of a function whose bound's function takes no value of
// its type parameter holds a call through the bound (ir.CalleeBound), in a
// module marked abstract, and no module the program runs holds one.
func TestAbstractBodies_HoldACallThroughTheBound(t *testing.T) {
	dir := writeProject(t, genericLibrary("[]", boundCases[0].src))
	p, err := Load(filepath.Join(dir, "genlib.nomi"))
	if err != nil {
		t.Fatalf("the library does not load: %v", err)
	}
	boundCalls := func(m *ir.Module) []string {
		var out []string
		for _, f := range m.Funcs() {
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					if c, ok := in.(*ir.Call); ok && c.Form() == ir.CalleeBound {
						out = append(out, f.Name()+": "+c.String())
					}
				}
			}
		}
		return out
	}
	for _, m := range p.res.IRModules() {
		if got := boundCalls(m); len(got) > 0 || m.Abstract() {
			t.Errorf("module %s, which the program runs, is abstract (%v) or holds bound calls %v", m.Name(), m.Abstract(), got)
		}
	}
	mods, declines := p.res.Abstract()
	if len(declines) != 0 {
		t.Errorf("declines %v; want none", declines)
	}
	var got []string
	for _, m := range mods {
		if !m.Abstract() {
			t.Errorf("abstract body module %s is not marked abstract", m.Name())
		}
		got = append(got, boundCalls(m)...)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "built: ") || !strings.Contains(got[0], "call bound Make.make()") {
		t.Errorf("bound calls %v; want built's one of Make.make", got)
	}
}

// consumerFiles is a library whose public generic function calls its
// interface's function through its bound, with no implementation of its
// own, and an app that depends on it, implements the interface with a
// read, and calls the function at that type. Each module's [effects.self]
// allows nothing.
func consumerFiles(appSelf string) map[string]string {
	return map[string]string{
		"genlib/nomi.toml": "[package]\nname = \"genlib\"\n\n[effects.self]\neffects = []\n",
		"genlib/genlib.nomi": `pub interface Make
    fn make() -> self
end

pub fn built<T>() -> T
where T: Make
    T.make()
end
`,
		"app/nomi.toml": "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\ngenlib = { dir = \"../genlib\" }\n\n[effects.self]\neffects = " +
			appSelf + "\n\n[effects.dependencies]\neffects = []\n",
		"app/main.nomi": `import
    std::fs
    std::io
    genlib.{self, Make}
end

struct Config
    text: String
end

impl Make for Config
    fn make() -> Config
        Config{text: fs.read_text("/etc/hosts") |> Result.with_default("")}
    end
end

fn main()
    c: Config = genlib.built()
    io.print(c.text)
end
`,
	}
}

// A library checked on its own answers a call through its bound by the
// implementations its program has, here none, so the call has no effect
// there: an implementation is another module's code, held where it is
// written. The consumer's impl reads a file, and the consumer's own
// [effects.self] holds it, reached from main through the instance of the
// library's function at the consumer's type; the library's list, and the
// consumer's [effects.dependencies], hold none of it.
func TestEffectRoots_AConsumersImplementationIsTheConsumers(t *testing.T) {
	checkedRootsPass(t, "genlib/genlib.nomi", consumerFiles(`["std::io"]`))
	got, ds := effectDiagnostics(t, "app/main.nomi", consumerFiles(`["std::io"]`))
	want := "app/main.nomi:13:22: this package's own code has the effect std::fs, and [effects.self] allows it only std::io\n" +
		"app/main.nomi:13:22: help: reached from `main`; `main` calls `built`; `built` calls `Make.make for Config`\n" +
		"app/main.nomi:13:22: help: add \"std::fs\" to effects under [effects.self] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || ds[0].Code != EffectNotAllowedCode {
		t.Errorf("diagnostics %v; want one %s", ds, EffectNotAllowedCode)
	}
	checkedRootsPass(t, "app/main.nomi", consumerFiles(`["std::fs", "std::io"]`))
}
