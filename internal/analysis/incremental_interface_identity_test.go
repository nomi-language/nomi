package analysis_test

import (
	"strings"
	"testing"
)

// showsProject is a program over two files that each declare an interface
// named Show (a, b). users declares User with an impl of a's. shapes
// re-exports a's Show, and work calls the Show shapes names and a's by its
// module.
func showsProject() incrementalProject {
	return incrementalProject{name: "shows", entry: "main", texts: map[string]string{
		"main": `import
    std::io
    work
end

fn main()
    io.print(work.run())
end
`,
		"a": `pub interface Show
    fn show(value: self) -> String
end
`,
		"b": `pub interface Show
    fn show(value: self) -> String
end
`,
		"shapes": `pub import a.{Show}
`,
		"users": `import a

pub struct User
    name: String
end

impl a.Show for User
    fn show(value: User) -> String
        "a ${value.name}"
    end
end
`,
		"work": `import
    a
    b
    shapes.{Show}
    users.{User}
end

pub fn run() -> String
    u = User{name: "ann"}
    Show.show(u) + a.Show.show(u)
end

pub fn other(s: b.Show) -> String
    b.Show.show(s)
end
`,
	}}
}

// showsEdits change which interface work's calls name: the re-export
// names b's Show, which User does not implement, and work's body calls b's
// by its module.
var showsEdits = []editStep{
	replace("the re-export names another Show [dependents]", "shapes", "a.{Show}", "b.{Show}"),
	restore("the re-export back [dependents]", "shapes"),
	replace("body only, a call of the other Show", "work", "a.Show.show(u)", "b.Show.show(u)"),
	restore("body back", "work"),
}

// TestIncrementalMatchesFullWhenAnInterfaceNameMeansAnother: an edit that
// makes a call name another interface of the same name, through the file
// that re-exports it or in the calling body, is checked again to the
// program a full check answers: the missing impl of b's Show appears and
// goes away with the edit.
func TestIncrementalMatchesFullWhenAnInterfaceNameMeansAnother(t *testing.T) {
	t.Parallel()
	p := showsProject()
	if prog := fullCheck(p, p.texts); len(prog.Errors["work"]) != 0 {
		t.Fatalf("the fixture does not check: %v", prog.Errors["work"])
	}
	for _, s := range []editStep{showsEdits[0], showsEdits[2]} {
		texts := map[string]string{}
		for k, v := range p.texts {
			texts[k] = v
		}
		changed, ok := s.change(texts[s.key], texts[s.key])
		if !ok {
			t.Fatalf("%q does not apply to the fixture", s.kind)
		}
		texts[s.key] = changed
		found := false
		for _, errs := range fullCheck(p, texts).Errors {
			for _, e := range errs {
				if strings.Contains(e.Message, "no impl of `b.Show` for `User`") {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("after %q, a full check does not report b's Show missing", s.kind)
		}
	}
	runChecked(t, p, showsEdits, newManager().RecheckWithOutcome)
}
