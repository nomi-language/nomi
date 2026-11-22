package vmhost

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// unreachedMain's main reaches greet alone. read_notes, summary, echo and
// blocked are reached by nothing: read_notes reads a file, summary calls
// it, echo is a generic function no call instantiates, and blocked's body
// the builder declines (plantDecline).
const unreachedMain = `import
    std::fs
    std::io
end

fn read_notes(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end

fn summary(path: String) -> String
    read_notes(path)
end

fn echo<T>(item: T) -> T
    io.print("echo")
    item
end

fn planted_decline(n: Int) -> Int
    n
end

fn blocked(path: String) -> Int
    io.print(path)
    planted_decline(1)
end

fn greet()
    io.print("hello")
end

fn main()
    greet()
end
`

// An editor's hover says what a function nothing reaches can do, as it
// says it of one main reaches: the walk an editor reads starts from every
// function the program's lowering holds that the checked walks do not
// reach (editorWalks). A function reached through such a function is
// marked too. A generic function no call instantiates is read from its
// abstract body, so it has its line as well; a body the builder declines
// has no line and no diagnostic. A function main reaches reads as it did.
func TestFunctionReach_FunctionsNothingReaches(t *testing.T) {
	plantDecline(t)
	dir := writeProject(t, map[string]string{
		"main.nomi": unreachedMain,
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	path := filepath.Join(dir, "main.nomi")
	found, err := CheckLowering(context.Background(), path, unreachedMain)
	if err != nil {
		t.Fatal(err)
	}
	if found.Problems != nil {
		t.Errorf("the check reports %v; a body nothing reaches is no problem, declined or not", found.Problems)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if calls := p.EffectCalls(Roots{}); slices.ContainsFunc(calls, func(c EffectCall) bool { return c.Name == "std::fs" }) {
		t.Errorf("the walk from main reaches std::fs (%+v); the walks the lists hold must stay rooted at main", calls)
	}
	fs := "Has effects: `std::fs`."
	io := "Has effects: `std::io`."
	for _, tc := range []struct {
		name, needle string
		want         []string
	}{
		{"the effect call in a function nothing reaches", "read_text", []string{fs}},
		{"the declaration of a function nothing reaches", "read_notes(path: String)", []string{fs}},
		{"a call of it in another function nothing reaches", "read_notes(path)\n", []string{fs}},
		{"the declaration of that function", "summary", []string{"Has effects: `std::fs` through `read_notes`."}},
		{"a generic function no call instantiates", "echo<T>", []string{io}},
		{"its effect call", "print(\"echo", []string{io}},
		{"a body the builder declines", "blocked", nil},
		{"an effect call in that body", "print(path", nil},
		{"a function main reaches", "greet()\n    io", []string{io}},
		{"main's call of it", "greet()\nend", []string{io}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hoverReach(t, p, dir, "main.nomi", tc.needle, 0); !slices.Equal(got, tc.want) {
				t.Errorf("hover on %q ends with\n%s\nwant\n%s", tc.needle, strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
	var marked []string
	for _, r := range found.Reached {
		marked = append(marked, strings.TrimPrefix(r.Text, "Has effects: "))
	}
	if !slices.ContainsFunc(found.Reached, func(r ReachedCall) bool { return r.Text == fs }) {
		t.Errorf("the editor's lowering finds no std::fs call; it found %q", marked)
	}
}

// unreachedTestsMain's helper reads a file, which its nomi.toml allows
// only in tests, and only a test calls it; stray, which nothing reaches,
// calls it too.
const unreachedTestsMain = `import
    std::fs
    std::io
end

fn helper(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end

fn stray() -> String
    helper("notes.txt")
end

fn main()
    io.print("hello")
end

test "the helper reads"
    assert helper("missing.txt") == ""
end
`

// The walk from what the checked walks do not reach holds its calls to the
// lists outside tests, and fills only the places they leave without a
// line: helper, which only a test reaches, reads as the test walk says,
// allowed by std_in_tests, though stray's call of it reads refused.
func TestFunctionReach_AFunctionNothingReachesChangesNoReachedLine(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"main.nomi": unreachedTestsMain,
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = [\"std::io\"]\nstd_in_tests = [\"std::fs\"]\n",
		"go.mod":    "module example.com/app\n\ngo 1.27.0\n",
	})
	p, err := Load(filepath.Join(dir, "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	allowed := "Has effects: `std::fs`, which `nomi.toml` allows in `[effects.self]`."
	for _, tc := range []struct {
		name, needle string
		want         []string
	}{
		{"the effect call only a test reaches", "read_text", []string{allowed}},
		{"the declaration only a test reaches", "helper(path", []string{allowed}},
		{"the test's call", "helper(\"missing", []string{allowed}},
		{"the call in a function nothing reaches", "helper(\"notes", []string{"Has effects: `std::fs`, which `nomi.toml` does not allow in `[effects.self]`."}},
		{"the function nothing reaches", "stray", []string{"Has effects: `std::fs` through `helper`, which `nomi.toml` does not allow in `[effects.self]`."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hoverReach(t, p, dir, "main.nomi", tc.needle, 0); !slices.Equal(got, tc.want) {
				t.Errorf("hover on %q ends with\n%s\nwant\n%s", tc.needle, strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}
