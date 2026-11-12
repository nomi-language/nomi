package analysis_test

import (
	"strings"
	"testing"
)

// A typealias another file declares names its target in every position
// where a type is written, however the files are ordered. A struct field,
// an enum payload, an interface function, a distinct type's inner type, a
// bound and a typealias are typed before the files' signatures, and a file
// typed before the one declaring the alias read it unbuilt: `kind:
// Maybe<Kind>` was `unknown type "Kind"`, and a literal of the struct
// `Form has no field 'kind'`. A function's parameter, typed later, always
// worked.
//
// Each case is checked with the alias declared in a.nomi and in z.nomi
// (checkEveryOrder), against interfaces (fromInterfacesDiff), and after an
// edit of a body and of the alias's own file (runEdits).
func TestTypeAliasFromAnotherFile_EveryPosition(t *testing.T) {
	schema := `pub enum Reaction
    Heart
    Pray
end

impl Display for Reaction
    fn to_string(value: Reaction) -> String
        case value
            .Heart -> "heart"
            .Pray -> "pray"
        end
    end
end

pub interface Shows
    fn shown(value: self) -> String
end

pub interface Holds<T>
    fn held(value: self) -> T
end
`
	aliases := `import schema

pub typealias Kind schema.Reaction

pub typealias Showing schema.Shows and Display

pub fn heart() -> Kind
    .Heart
end
`
	for _, c := range []struct {
		name string
		// uses is the file that names the alias, and main the entry that
		// reads what uses declares.
		uses, main string
	}{
		{
			name: "struct field",
			uses: `import %s.Kind

pub struct Form
    kind: Maybe<Kind>
    all: List<Kind>
end
`,
			main: `import
    std::io
    uses.Form
end

fn main()
    form = Form{kind: Some(.Heart), all: [.Pray]}
    io.print("${form.kind == Some(.Heart)} ${form.all}")
end
`,
		},
		{
			name: "struct field, qualified",
			uses: `import %s

pub struct Form
    kind: %s.Kind
end
`,
			main: `import
    std::io
    uses.Form
end

fn main()
    form = Form{kind: .Pray}
    io.print("${form.kind}")
end
`,
		},
		{
			name: "enum payload",
			uses: `import %s.Kind

pub enum Event
    Reacted(Kind)
    Named {kind: Kind}
    Nothing
end
`,
			main: `import
    std::io
    uses.Event
end

fn main()
    event = Event.Reacted(.Heart)
    named = Event.Named{kind: .Pray}
    io.print(case event
        .Reacted(.Heart) -> "heart"
        _ -> "other"
    end)
    io.print(case named
        .Named{kind: .Pray} -> "pray"
        _ -> "other"
    end)
end
`,
		},
		{
			name: "interface function",
			uses: `import %s.Kind

pub interface Judge
    fn judge(value: self, kind: Kind) -> Kind
end

pub struct Judge1
    n: Int
end

impl Judge for Judge1
    fn judge(value: Judge1, kind: Kind) -> Kind
        if value.n > 0 then kind else .Pray
    end
end
`,
			main: `import
    std::io
    uses.{Judge, Judge1}
end

fn main()
    io.print("${Judge.judge(Judge1{n: 1}, .Heart)}")
end
`,
		},
		{
			name: "distinct type",
			uses: `import %s.Kind

pub type Tagged Kind
`,
			main: `import
    std::io
    uses.Tagged
end

fn main()
    Tagged(kind) = Tagged(.Pray)
    io.print("${kind}")
end
`,
		},
		{
			name: "bound",
			uses: `import
    %s.Showing
    schema.Shows
end

pub struct Shelf<T> where T: Showing
    item: T
end

pub struct Book
    title: String
end

impl Shows for Book
    fn shown(value: Book) -> String
        value.title
    end
end

impl Display for Book
    fn to_string(value: Book) -> String
        value.title
    end
end
`,
			main: `import
    std::io
    uses.{Book, Shelf}
end

fn main()
    shelf = Shelf{item: Book{title: "Emma"}}
    io.print("${shelf.item}")
end
`,
		},
		{
			name: "typealias of a typealias",
			uses: `import %s.Kind

pub typealias Pick Kind

pub struct Choice
    pick: Pick
end
`,
			main: `import
    std::io
    uses.{Choice, Pick}
end

fn main()
    choice = Choice{pick: .Heart}
    pick: Pick = choice.pick
    io.print("${pick}")
end
`,
		},
		{
			name: "impl header and once",
			uses: `import
    %s.Kind
    schema.Holds
end

pub struct Box
    kind: Kind
end

impl Holds<Kind> for Box
    fn held(value: Box) -> Kind
        value.kind
    end
end

pub once default_box: Box = Box{kind: .Heart}
`,
			main: `import
    std::io
    uses.default_box
    schema.Holds
end

fn main()
    io.print("${Holds.held(default_box)}")
end
`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			texts := map[string]string{"schema": schema, "%s": aliases, "uses": c.uses, "main": c.main}
			checkEveryOrder(t, texts)
			if t.Failed() {
				return
			}
			for _, name := range []string{"a", "z"} {
				named := map[string]string{}
				for key, text := range texts {
					named[strings.ReplaceAll(key, "%s", name)] = strings.ReplaceAll(text, "%s", name)
				}
				p := incrementalProject{name: "typealias/" + c.name + "/" + name, entry: "main", texts: named}
				if diff := fromInterfacesDiff(p, named); diff != "" {
					t.Errorf("declared in %s.nomi: checked against interfaces, the program differs from a full check:\n%s", name, diff)
				}
				steps := []editStep{
					appendText("a function in the file that uses the alias", "uses", "\npub fn extra() -> Int\n    1\nend\n"),
					replace("the alias's file", name, "    .Heart\n", "    .Pray\n"),
				}
				diff, _ := runEdits(t, p, steps, newManager().RecheckWithOutcome, false)
				if diff != "" {
					t.Errorf("declared in %s.nomi: %s", name, diff)
				}
			}
		})
	}
}
