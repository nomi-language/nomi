package analysis_test

import (
	"strings"
	"testing"
)

// A name another file re-exports with `pub import` is importable however the
// files are ordered. Each file defined its imports when its annotations were
// walked, one file at a time, so a file walked before the facade read the
// facade's scope without the re-exported name: `import a.Reaction` was "file
// 'a' has no exported name 'Reaction'". A re-export of a re-export and a
// facade in an import cycle wait the same way.
//
// Each case is checked with the facade in a.nomi and in z.nomi
// (checkEveryOrder): `a` and `ma` sort before main and uses, `mz` between
// main and uses, and `z` after every file. It is also checked against
// interfaces (fromInterfacesDiff) and after an edit of a body and of the
// facade (runEdits).
func TestReexport_EveryFileOrder(t *testing.T) {
	schema := `pub enum Reaction {
    Heart
    Pray
}

impl Display for Reaction {
    fn to_string(value: Reaction) -> String {
        case value {
            .Heart -> "heart"
            .Pray -> "pray"
        }
    }
}
`
	for _, c := range []struct {
		name  string
		texts map[string]string
	}{
		{
			name: "a re-exported enum",
			texts: map[string]string{
				"schema": schema,
				"%s": `pub import schema.Reaction

pub fn first() -> Int {
    1
}
`,
				"uses": `import %s.Reaction

pub struct Form {
    kind: Reaction
}

pub fn heart() -> Reaction {
    .Heart
}
`,
				"main": `import {
    std::io
    %s.{self, Reaction}
    uses.{Form, heart}
}

fn main() {
    form = Form{kind: heart()}
    r: %s.Reaction = form.kind
    p: Reaction = .Pray
    io.print("${r} ${p}")
}
`,
			},
		},
		{
			name: "a re-export of a re-export",
			texts: map[string]string{
				"schema": schema,
				"%s": `pub import schema.Reaction

pub fn first() -> Int {
    1
}
`,
				"m%s": `pub import %s.Reaction as Kind
`,
				"main": `import {
    std::io
    m%s.Kind
}

fn main() {
    k: Kind = .Heart
    io.print("${k}")
}
`,
			},
		},
		{
			name: "a facade in an import cycle",
			texts: map[string]string{
				"schema": schema,
				"%s": `pub import {
    schema.Reaction
    uses.Mood
}

pub fn first() -> Int {
    1
}
`,
				"uses": `import %s.{Reaction, first}

pub enum Mood {
    Calm
    Glad
}

pub fn mood(r: Reaction) -> Mood {
    if first() > 0 and r == .Heart { .Glad } else { .Calm }
}
`,
				"main": `import {
    std::io
    %s.{Mood, Reaction}
    uses.mood
}

fn main() {
    m: Mood = mood(Reaction.Heart)
    io.print(case m {
        .Glad -> "glad"
        .Calm -> "calm"
    })
}
`,
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			checkEveryOrder(t, c.texts)
			if t.Failed() {
				return
			}
			for _, name := range []string{"a", "z"} {
				named := map[string]string{}
				for key, text := range c.texts {
					named[strings.ReplaceAll(key, "%s", name)] = strings.ReplaceAll(text, "%s", name)
				}
				p := incrementalProject{name: "reexport/" + c.name + "/" + name, entry: "main", texts: named}
				if diff := fromInterfacesDiff(p, named); diff != "" {
					t.Errorf("facade in %s.nomi: checked against interfaces, the program differs from a full check:\n%s", name, diff)
				}
				steps := []editStep{
					appendText("a function in the entry", "main", "\npub fn extra() -> Int {\n    1\n}\n"),
					replace("the facade", name, "    1\n", "    2\n"),
				}
				diff, _ := runEdits(t, p, steps, newManager().RecheckWithOutcome, false)
				if diff != "" {
					t.Errorf("facade in %s.nomi: %s", name, diff)
				}
			}
		})
	}
}
