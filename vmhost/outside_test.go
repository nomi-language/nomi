package vmhost_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

// outsideLines is each outside call p reaches from r as one line: its
// name, the module charged ("program" for the program's own files), its
// site, its callee, how the site reaches the callee, and the chain to the
// function holding it, each step with the edge that reached it.
func outsideLines(p *vmhost.Program, r vmhost.Roots) []string {
	var out []string
	for _, c := range p.OutsideCalls(r) {
		module := c.Module
		if module == "" {
			module = "program"
		}
		steps := make([]string, len(c.Chain))
		for i, s := range c.Chain {
			steps[i] = fmt.Sprintf("%s(%s)", s.Func, s.Edge)
		}
		out = append(out, fmt.Sprintf("%s | %s | %s | %s %s | %s", c.Name, module, c.Site, c.Edge, c.Callee, strings.Join(steps, " > ")))
	}
	return out
}

// loadProject writes files under a fresh directory and loads entry from it.
func loadProject(t *testing.T, entry string, files map[string]string, opts ...vmhost.Option) *vmhost.Program {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := vmhost.Load(filepath.Join(dir, filepath.FromSlash(entry)), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func checkOutside(t *testing.T, p *vmhost.Program, r vmhost.Roots, want ...string) {
	t.Helper()
	got := outsideLines(p, r)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("outside calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// One program per kind of outside call, each reaching exactly it, named by
// the std module the program called and charged to the program.
func TestOutsideCalls_EachKindOfOutsideCall(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"files", `import
    std::fs
    std::io
end

fn main() -> Result<Unit, io.Error>
    _ = fs.read_text("notes.txt")?
    Ok(Unit)
end
`, []string{"std::fs | program | main.nomi:7:21 | call fs.read_text | main(root)"}},
		{"network client", `import
    std::http
    std::url.Url
end

fn main() -> Result<Unit, http.Error>
    _ = http.get(Url.parse("https://example.com")!)?
    Ok(Unit)
end
`, []string{"std::http | program | main.nomi:7:17 | call http.get | main(root)"}},
		{"server", `import std::http

fn main() -> Result<Unit, http.Error>
    _ = http.listen()?
    Ok(Unit)
end
`, []string{"std::http | program | main.nomi:4:20 | call http.listen | main(root)"}},
		{"process", `import
    std::io
    std::process.{self, Command}
end

fn main() -> Result<Unit, io.Error>
    _ = process.run(Command{program: "make"})?
    Ok(Unit)
end
`, []string{"std::process | program | main.nomi:7:20 | call process.run | main(root)"}},
		{"output", `import std::io

fn main()
    io.print("hi")
end
`, []string{"std::io | program | main.nomi:4:13 | call io.print | main(root)"}},
		{"stdin", `import std::io

fn main()
    _ = io.read_line()
end
`, []string{"std::io | program | main.nomi:4:21 | call io.read_line | main(root)"}},
		{"clock", `import std::instant.Instant

fn main()
    _ = Instant.now()
end
`, []string{"std::instant | program | main.nomi:4:20 | call instant.Instant.now | main(root)"}},
		{"entropy", `import std::random.Seed

fn main()
    _ = Seed.from_os()
end
`, []string{"std::random | program | main.nomi:4:21 | call Seed.from_os | main(root)"}},
		{"environment and arguments", `struct App
    greeting: String
end

fn boot(startup: Startup) -> Result<App, String>
    Ok(App{greeting: Map.get(startup.env, "GREETING") |> Maybe.with_default("hello")})
end

fn main()
    _ = App.greeting
end
`, []string{"std::startup | program | main.nomi:5:4 | boot boot | boot(boot)"}},
		{"task", `import std::tasks.Task

fn main()
    _ = concurrent
        t = Task.spawn(fn -> 1)
        Task.await(t)
    end
end
`, []string{"std::tasks | program | main.nomi:5:23 | call Task.spawn | main(root) > concurrent(value)"}},
		{"nested program", `import std::compiler

fn main()
    _ = compiler.run("fn main() end")
end
`, []string{"std::compiler | program | main.nomi:4:21 | call compiler.run | main(root)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := loadProject(t, "main.nomi", map[string]string{"main.nomi": c.src})
			checkOutside(t, p, vmhost.Roots{}, c.want...)
		})
	}
}

// std::net and std::tls cross through http's host functions; each call is
// named by the module the program called.
func TestOutsideCalls_NamedByTheModuleTheProgramCalled(t *testing.T) {
	p := loadProject(t, "main.nomi", map[string]string{"main.nomi": `import
    std::net
    std::tls
end

fn main()
    _ = net.dial("example.com:80")
    _ = tls.dial("example.com:443")
end
`})
	checkOutside(t, p, vmhost.Roots{},
		"std::net | program | main.nomi:7:17 | call net.dial | main(root)",
		"std::tls | program | main.nomi:8:17 | call tls.dial | main(root)",
	)
}

// A seeded random generator and a date formatter reach nothing outside, and
// neither does a program that only computes.
func TestOutsideCalls_InsideCallsReachNothing(t *testing.T) {
	for name, src := range map[string]string{
		"seeded generator and formatter": `import
    std::random.{Generator, Seed}
    std::calendar.Date
end

fn main()
    (roll, _) = Generator.step(Generator.int(1..=6), Seed.from_int(123))
    _ = roll
    _ = Date.new(2026, 5, 4) |> Result.map(fn d -> Date.format(d, Locale"en"))
end
`,
		"pure": `fn main()
    _ = [1, 2, 3] |> Iter.map(fn n -> n * 2) |> Iter.to_list()
end
`,
	} {
		t.Run(name, func(t *testing.T) {
			checkOutside(t, loadProject(t, "main.nomi", map[string]string{"main.nomi": src}), vmhost.Roots{})
		})
	}
}

// Each call is charged to the module whose file holds it: a dependency's
// own read to the dependency, a callback the program passes the dependency
// to the program, and the instance of a dependency's generic function the
// program asks for to the dependency.
func TestOutsideCalls_ChargedToTheModuleWhoseFileHoldsTheCall(t *testing.T) {
	p := loadProject(t, "app/main.nomi", map[string]string{
		"app/main.nomi": `import
    std::fs
    pricing::rules
end

fn read(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end

fn main()
    _ = rules.load("prices.txt")
    _ = rules.each(["a.txt"], read)
    _ = rules.twice(1, fn n -> n + 1)
end
`,
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"app/go.mod":    "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/pricing v0.0.0\n\nreplace example.com/pricing => ../pricing\n",
		"pricing/rules.nomi": `import
    std::fs
    std::io
end

pub fn load(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end

pub fn each(paths: List<String>, f: (String) -> String) -> List<String>
    paths |> Iter.map(f) |> Iter.to_list()
end

pub fn twice<T>(x: T, f: (T) -> T) -> T
    io.print("twice")
    f(f(x))
end
`,
		"pricing/nomi.toml": "[module]\nname = \"pricing\"\n",
		"pricing/go.mod":    "module example.com/pricing\n\ngo 1.27.0\n",
	})
	checkOutside(t, p, vmhost.Roots{},
		"std::fs | program | main.nomi:7:17 | call fs.read_text | main(root) > read(value)",
		"std::fs | example.com/pricing | rules.nomi:7:17 | call fs.read_text | main(root) > load(call)",
		"std::io | example.com/pricing | rules.nomi:15:13 | call io.print | main(root) > twice(call)",
	)
}

// The walk is conservative, and each chain says why a call counts: a call
// in a branch never taken, a function passed as a value and never called,
// an implementation whose type is never built, and a Display body reached
// because main renders a value held as a Display. A module imported and never called reaches
// nothing.
func TestOutsideCalls_EdgeKindsSayWhyACallCounts(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"a branch never taken", `import
    std::io
    std::process.{self, Command}
end

fn main()
    n = 1
    if n > 5 then
        io.print(process.run(Command{program: "ls"}))
    end
    io.print("done")
end
`, []string{
			"std::io | program | main.nomi:9:17 | call io.print | main(root)",
			"std::process | program | main.nomi:9:29 | call process.run | main(root)",
			"std::io | program | main.nomi:11:13 | call io.print | main(root)",
		}},
		{"a value never called", `import
    std::io
    std::process.{self, Command}
end

fn ls(name: String) -> String
    "${process.run(Command{program: name})}"
end

fn ignore(_f: (String) -> String) -> Int
    1
end

fn main()
    io.print(ignore(ls))
end
`, []string{
			"std::process | program | main.nomi:7:19 | call process.run | main(root) > ls(value)",
			"std::io | program | main.nomi:15:13 | call io.print | main(root)",
		}},
		{"an implementation never built", `import
    std::io
    std::process.{self, Command}
end

interface Runner
    fn go(r: self) -> String
end

struct Harmless
    x: Int
end

struct Shell
    program: String
end

impl Runner for Harmless
    fn go(r: Harmless) -> String
        "harmless ${r.x}"
    end
end

impl Runner for Shell
    fn go(r: Shell) -> String
        "${process.run(Command{program: r.program})}"
    end
end

fn use(r: Runner) -> String
    Runner.go(r)
end

fn main()
    io.print(use(Harmless{x: 1}))
end
`, []string{
			"std::process | program | main.nomi:26:23 | call process.run | main(root) > use(call) > Runner.go for Shell(dispatch)",
			"std::io | program | main.nomi:35:13 | call io.print | main(root)",
		}},
		{"a module never called", `import
    std::io
    std::process.{Command}
end

fn main()
    c = Command{program: "ls"}
    io.print(c.program)
end
`, []string{"std::io | program | main.nomi:8:13 | call io.print | main(root)"}},
		{"a Display body", `import
    std::io
    std::process.{self, Command}
end

struct Shell
    program: String
end

impl Display for Shell
    fn to_string(s: Shell) -> String
        "${process.run(Command{program: s.program})}"
    end
end

fn main()
    io.print(show(Shell{program: "ls"}))
end

fn show(d: Display) -> String
    "${d}"
end
`, []string{
			"std::process | program | main.nomi:12:23 | call process.run | main(root) > show(call) > Display.to_string for Shell(display)",
			"std::io | program | main.nomi:17:13 | call io.print | main(root)",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkOutside(t, loadProject(t, "main.nomi", map[string]string{"main.nomi": c.src}), vmhost.Roots{}, c.want...)
		})
	}
}

// Every way the walk reaches a function is a kind of edge, and each one
// shows on a chain: one program reaches an outside call through each kind,
// with its tests for EdgeTest. A kind no chain shows, or a step with no
// kind, fails here.
func TestOutsideCalls_EveryEdgeKindIsReported(t *testing.T) {
	p := loadProject(t, "main.nomi", map[string]string{"main.nomi": `import std::io

struct App
    name: String
end

fn boot(startup: Startup) -> Result<App, String>
    io.print("boot")
    Ok(App{name: List.first(startup.args) |> Maybe.with_default("app")})
end

interface Greeter
    fn greet(g: self) -> String
end

struct Loud
    n: Int
end

impl Greeter for Loud
    fn greet(g: Loud) -> String
        io.print("dispatch")
        "${g.n}"
    end
end

impl Display for Loud
    fn to_string(_g: Loud) -> String
        io.print("display")
        "loud"
    end
end

impl Debug for Loud
    fn inspect(_g: Loud) -> String
        io.print("debug")
        "Loud"
    end
end

impl Equatable for Loud
    fn equal(a: Loud, b: Loud) -> Bool
        io.print("keys")
        a.n == b.n
    end
end

fn counted() -> Int
    io.print("once")
    3
end

once limit = counted()

fn helper()
    io.print("call")
end

fn show(d: Display) -> String
    "${d}"
end

fn greet_all(g: Greeter) -> String
    Greeter.greet(g)
end

fn main()
    helper()
    run = fn -> io.print("value")
    run()
    _ = greet_all(Loud{n: limit})
    io.print(show(Loud{n: 3}))
    io.inspect(Loud{n: 2})
    _ = [Loud{n: 1}] == [Loud{n: 2}]
end

test "a case"
    io.print("test")
    assert [Loud{n: limit}] == [Loud{n: 3}]
end
`})
	seen := map[vmhost.Edge]bool{}
	for _, r := range []vmhost.Roots{{}, {Tests: &vmhost.TestOptions{}}} {
		for _, c := range p.OutsideCalls(r) {
			seen[c.Edge] = true
			for _, s := range c.Chain {
				seen[s.Edge] = true
			}
		}
	}
	for _, k := range vm.EdgeKinds() {
		if !seen[k] {
			t.Errorf("no chain shows an edge of kind %s", k)
		}
		delete(seen, k)
	}
	for k := range seen {
		t.Errorf("a chain shows edge kind %d, which EdgeKinds does not list", k)
	}
}

// A `go` binding is an outside call named by the Go package its `gopkg`
// imports, read from the declaring file; an embedder's own `host fn`, which
// binds no Go package, is named by its key. The tables here stand in for
// the FFI wrapper's and the embedder's, so no Go is built.
func TestOutsideCalls_GoBindingsAreNamedByTheirPackage(t *testing.T) {
	answer := func(key string) vmhost.HostTable {
		return func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
			return map[string]hostadapt.Func{key: func(_ *rt.Frame, args []rt.Value) (rt.Value, error) { return args[0], nil }}, nil
		}
	}
	p := loadProject(t, "main.nomi", map[string]string{
		"go.mod": "module app\n\ngo 1.27.0\n",
		"main.nomi": `gopkg "app/ffi" as ffi

fn upper(s: String) -> String go ffi.Upper

fn main()
    _ = upper("hi")
end
`,
	}, vmhost.WithHosts(answer("upper")))
	checkOutside(t, p, vmhost.Roots{},
		"go:app/ffi | program | main.nomi:3:4 | call upper | main(root) > upper(call)",
	)

	embedded, err := vmhost.LoadSource("main", "host fn shout(s: String) -> String\n\nfn main()\n    _ = shout(\"hi\")\nend\n",
		vmhost.WithHosts(answer("shout")))
	if err != nil {
		t.Fatal(err)
	}
	checkOutside(t, embedded, vmhost.Roots{},
		"host:shout | program | main.nomi:1:9 | call shout | main(root) > shout(call)",
	)
}

// Test cases are roots of their own kind, and a group's boot is reached as
// a boot; a Startup a test builds crosses nothing.
func TestOutsideCalls_FromTests(t *testing.T) {
	p := loadProject(t, "main.nomi", map[string]string{"main.nomi": `import std::io

struct App
    greeting: String
end

fn boot(startup: Startup) -> Result<App, String>
    io.print("booting")
    Ok(App{greeting: Map.get(startup.env, "GREETING") |> Maybe.with_default("hello")})
end

fn main()
    io.print(App.greeting)
end

group "greetings"
    boot boot(Startup{env: {"GREETING" => "hi"}})

    test "greets"
        io.print(App.greeting)
        assert App.greeting == "hi"
    end
end
`})
	checkOutside(t, p, vmhost.Roots{Tests: &vmhost.TestOptions{}},
		"std::io | program | main.nomi:8:13 | call io.print | boot(boot)",
		"std::io | program | main.nomi:20:17 | call io.print | greetings / greets(test)",
	)
}
