package vmhost

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/hoverdoc"
)

// hoverReach is what a hover at the first character of the occurrence of
// needle given by nth (0 for the first) in the file named file of the
// program at entry ends with, as an editor and the docs site show it
// (reachedCalls, hoverdoc.AppendReach): each reach line, one per element.
func hoverReach(t *testing.T, p *Program, dir, file, needle string, nth int) []string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(file))
	text, err := p.sourceOf(path)
	if err != nil {
		t.Fatal(err)
	}
	at := -1
	for i := 0; i <= nth; i++ {
		next := strings.Index(text[at+1:], needle)
		if next < 0 {
			t.Fatalf("%s has no occurrence %d of %q", file, nth, needle)
		}
		at += 1 + next
	}
	line := strings.Count(text[:at], "\n") + 1
	col := at - strings.LastIndex(text[:at], "\n")
	pos := analysis.Pos{Line: line, Col: col}
	shown := hoverdoc.AppendReach("", p.reachedCalls(p.walks(p.checkedRoots())), func(c string) bool { return samePathForTest(c, path) }, pos, pos)
	var out []string
	for _, l := range strings.Split(shown, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// linesMatch reports whether got is the reach line want, in which `*`
// stands for the name of an impl's function, whose form this test does
// not hold.
func linesMatch(got, want string) bool {
	re := regexp.QuoteMeta(want)
	re = strings.ReplaceAll(re, "`\\*`", "`[^`]+`")
	return regexp.MustCompile("^" + re + "$").MatchString(got)
}

func samePathForTest(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return a == b || ra == rb
}

// functionReachMain calls load_config, which reaches std::fs two calls
// down; report, which reaches std::io and std::fs; double, which reaches
// nothing; and shout, a generic function whose instances reach std::io
// and, for File, std::fs.
const functionReachMain = `import
    std::fs
    std::io
end

fn read_settings(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end

fn load_config() -> String
    read_settings("app.conf")
end

fn report(text: String)
    io.print(load_config())
    io.print(text)
end

fn double(n: Int) -> Int
    n * 2
end

interface Named
    fn name(value: self) -> String
end

struct File
    path: String
end

impl Named for File
    fn name(file: File) -> String
        read_settings(file.path)
    end
end

impl Named for Int
    fn name(n: Int) -> String
        "${n}"
    end
end

fn shout<T>(item: T) -> T where T: Named
    io.print(T.name(item))
    item
end

fn main()
    report("${double(2)}")
    _ = shout(1)
    _ = shout(File{path: "a.txt"})
end
`

// A function of the program, and a call of it, end their hover with what
// the function can reach outside the machine, each std module once,
// through the functions on the way, and whether nomi.toml's lists allow
// it; a function that reaches nothing has no line.
func TestFunctionReach_OwnFunctions(t *testing.T) {
	const module = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"
	dir := writeProject(t, map[string]string{
		"main.nomi": functionReachMain,
		"nomi.toml": module + "\n[capabilities.self]\nstd = [\"std::io\"]\n",
		"go.mod":    "module example.com/app\n\ngo 1.27.0\n",
	})
	p, err := Load(filepath.Join(dir, "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	refusedFs := "`nomi.toml` does not allow in `[capabilities.self]`."
	allowedIo := "Reaches `std::io`, which `nomi.toml` allows in `[capabilities.self]`."
	alsoIo := "Also reaches `std::io`."
	for _, tc := range []struct {
		name, needle string
		nth          int
		want         []string
	}{
		{"a call two calls above the outside call", "load_config()", 1, []string{"Reaches `std::fs` through `read_settings`, which " + refusedFs}},
		{"the declaration's name", "load_config", 0, []string{"Reaches `std::fs` through `read_settings`, which " + refusedFs}},
		{"the function that makes the call", "read_settings(path", 0, []string{"Reaches `std::fs`, which " + refusedFs}},
		{"two modules, the refused first", "report(text", 0, []string{"Reaches `std::fs` through `load_config`, `read_settings`, which " + refusedFs, alsoIo}},
		{"a call of a function of two modules", "report(\"", 0, []string{"Reaches `std::fs` through `load_config`, `read_settings`, which " + refusedFs, alsoIo}},
		{"a pure function", "double(n", 0, nil},
		{"a call of a pure function", "double(2", 0, nil},
		{"a generic function's declaration holds every instance's", "shout<T>", 0, []string{"Reaches `std::fs` through `*`, `read_settings`, which " + refusedFs, alsoIo}},
		{"a generic function's call of one instance", "shout(1)", 0, []string{allowedIo}},
		{"a generic function's call of another", "shout(File", 0, []string{"Reaches `std::fs` through `*`, `read_settings`, which " + refusedFs, alsoIo}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hoverReach(t, p, dir, "main.nomi", tc.needle, tc.nth); !slices.EqualFunc(got, tc.want, linesMatch) {
				t.Errorf("hover on %q ends with\n%s\nwant\n%s", tc.needle, strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// A dependency's function, and the program's call of it, say what it can
// reach as the call in its file does, with the tables of the project that
// hold the dependency's code; with no tables the line says only what it
// reaches.
func TestFunctionReach_ADependencysFunction(t *testing.T) {
	for _, tc := range []struct {
		name, app, want string
	}{
		{"no tables", "[module]\nname = \"app\"\nentry_points = [\"main\"]\n", "Reaches `std::fs`."},
		{"held by deps and its entry", "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.deps]\nstd = [\"std::fs\"]\n\n" +
			"[capabilities.deps.modules]\n\"example.com/pricing\" = [\"std::fs\"]\n",
			"Reaches `std::fs`, which `nomi.toml` allows in `[capabilities.deps]` and in the entry for `example.com/pricing`."},
		{"refused by its entry", "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.deps.modules]\n\"example.com/pricing\" = []\n",
			"Reaches `std::fs`, which `nomi.toml` does not allow in the entry for `example.com/pricing`."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeProject(t, dependencyFiles(tc.app, ""))
			p, err := Load(filepath.Join(dir, "app", "main.nomi"))
			if err != nil {
				t.Fatal(err)
			}
			for _, at := range []struct{ file, needle string }{{"app/main.nomi", "rules.load"}, {"pricing/rules.nomi", "load(path"}} {
				if got := hoverReach(t, p, dir, at.file, at.needle, 0); !slices.Equal(got, []string{tc.want}) {
					t.Errorf("hover on %q in %s ends with %q; want %q", at.needle, at.file, got, tc.want)
				}
			}
		})
	}
}
