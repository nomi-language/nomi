package vmhost

import (
	"path/filepath"
	"slices"
	"testing"
)

// onceReachStreams writes to the standard streams through io.stdout and
// io.stderr, std's `once` values whose initializers ask for the streams:
// the reads have the effect, and report and Writer.print act only on the
// handle passed to them.
const onceReachStreams = `import std::io.{self, Error, Writer}

fn report(to: Writer, count: Int) -> Result<Unit, Error>
    to |> Writer.print("${count} items")
end

fn main() -> Result<Unit, Error>
    report(io.stdout, 3)?
    report(io.stderr, 0)?
    Ok(Unit)
end
`

// A read of a std `once` whose initializer has effects ends its hover with
// the effect, on the name it reads, as a call's does, and is not marked:
// it is a value. The function the handle is passed to, its calls and the
// interface function it calls say they act on what they are passed, and
// are marked, and so is the function holding the calls, whose hover names
// the effect of the reads.
func TestOnceReach_StdStreams(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"main.nomi": onceReachStreams,
		"nomi.toml": "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\neffects = [\"std::io\"]\n",
	})
	p, err := Load(filepath.Join(dir, "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	io := "Has effects: `std::io`, which `nomi.toml` allows in `[effects.self]`."
	held := "Has effects on what it is passed."
	for _, tc := range []struct {
		needle string
		nth    int
		want   []string
	}{
		{"stdout", 0, []string{io}},
		{"stderr", 0, []string{io}},
		{"main", 0, []string{io}},
		{"io.stdout", 0, nil},
		{"report", 0, []string{held}},
		{"report", 1, []string{held}},
		{"print", 0, []string{held}},
		{"to |>", 0, nil},
	} {
		if got := hoverReach(t, p, dir, "main.nomi", tc.needle, tc.nth); !slices.Equal(got, tc.want) {
			t.Errorf("hover on %q (occurrence %d) ends with %q, want %q", tc.needle, tc.nth, got, tc.want)
		}
	}
	want := []string{"3:report", "4:Writer.print", "7:main", "8:report", "9:report"}
	if got := markedSpans(t, p, dir, "main.nomi"); !slices.Equal(got, want) {
		t.Errorf("the marked places are %q, want %q", got, want)
	}
}

// onceReachOwn declares onces of its own: settings, whose initializer reads
// a file; banner, whose initializer reads settings; limit, which is pure;
// and log_text, which nothing reads. Onces are read by their bare names,
// qualified by another file's name (shout.prefix), through an import
// alias (input, std's stdin) and through a file's alias (sys.stderr);
// unused reads input in a function nothing calls; greeting reads an app
// field, which is no effect; main passes io.print as a value.
const onceReachOwn = `import
    std::fs
    std::io.{self, stdin as input}
    shout
end

struct App
    name: String
end

fn boot() -> Result<App, String>
    Ok(App{name: "Ada"})
end

once settings: String = fs.read_text("settings.txt") |> Result.with_default("")

once banner: String = "${settings}!"

once limit = 6

once log_text: String = fs.read_text("log.txt") |> Result.with_default("")

fn greeting() -> String
    "hello, ${App.name}"
end

fn unused() -> Bool
    _ = input
    True
end

fn main()
    io.print(banner)
    io.print("${limit} ${greeting()}")
    shout.loud(shout.prefix)
    ["a", "b"] |> Iter.each(io.print)
end
`

const onceReachShout = `import
    std::fs
    std::io as sys
end

pub once prefix: String = fs.read_text("prefix.txt") |> Result.with_default("")

pub fn loud(text: String)
    sys.stderr |> sys.Writer.print(text) |> Result.with_default(Unit)
end
`

// A `once` of the program whose initializer has effects has a line as a
// function does: its declaration's name and each read of it end their hover
// with the effects, a once read inside another's initializer included, and
// a once nothing reads too. A pure once, and an app field read, have no
// line. None of them is marked, and neither is a std function named as a
// value: the calls in the initializers are, and the calls that can act.
func TestOnceReach_OwnOnces(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"main.nomi":  onceReachOwn,
		"shout.nomi": onceReachShout,
		"nomi.toml":  "[package]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	p, err := Load(filepath.Join(dir, "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	fs := "Has effects: `std::fs`."
	io := "Has effects: `std::io`."
	throughSettings := "Has effects: `std::fs` through `once settings`."
	for _, tc := range []struct {
		file, needle string
		nth          int
		want         []string
		what         string
	}{
		{"main.nomi", "once settings", 0, nil, "the `once` keyword"},
		{"main.nomi", "settings", 0, []string{fs}, "a once's declaration"},
		{"main.nomi", "settings}", 0, []string{fs}, "a read of a once in another once's initializer"},
		{"main.nomi", "banner", 0, []string{throughSettings}, "the declaration of a once whose initializer reads one"},
		{"main.nomi", "banner)", 0, []string{throughSettings}, "a read of that once"},
		{"main.nomi", "limit", 0, nil, "a pure once's declaration"},
		{"main.nomi", "limit}", 0, nil, "a read of a pure once"},
		{"main.nomi", "log_text", 0, []string{fs}, "a once nothing reads"},
		{"main.nomi", "input\n", 0, []string{io}, "a read of a std once through an alias, in a function nothing calls"},
		{"main.nomi", "stdin as input", 0, nil, "the import of the alias"},
		{"main.nomi", "name}", 0, nil, "an app field read"},
		{"main.nomi", "greeting", 0, []string{"Uses `App.name`."}, "a function that reads an app field"},
		{"main.nomi", "prefix", 0, []string{fs}, "a qualified read of another file's once"},
		{"main.nomi", "shout.prefix", 0, nil, "the qualifier of that read"},
		{"main.nomi", "print)", 0, []string{io}, "a std function used as a value, which is shown as a qualified read is"},
		{"shout.nomi", "prefix", 0, []string{fs}, "that once's declaration"},
		{"shout.nomi", "stderr", 0, []string{io}, "a read of a std once through the file's alias"},
		{"shout.nomi", "loud", 0, []string{io}, "the function holding it"},
	} {
		if got := hoverReach(t, p, dir, tc.file, tc.needle, tc.nth); !slices.Equal(got, tc.want) {
			t.Errorf("%s (%q in %s): hover ends with %q, want %q", tc.what, tc.needle, tc.file, got, tc.want)
		}
	}
	for file, want := range map[string][]string{
		"main.nomi":  {"15:fs.read_text", "21:fs.read_text", "32:main", "33:io.print", "34:io.print", "35:shout.loud", "36:Iter.each"},
		"shout.nomi": {"6:fs.read_text", "8:loud", "9:sys.Writer.print"},
	} {
		if got := markedSpans(t, p, dir, file); !slices.Equal(got, want) {
			t.Errorf("the marked places in %s are %q, want %q", file, got, want)
		}
	}
}
