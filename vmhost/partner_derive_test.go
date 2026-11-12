package vmhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// apiSource declares a private wire type that only encodes, and a private
// Tag its field names, for the partner-test derive tests.
const apiSource = `import std::json.{Json, ToJson}

struct Tag
    name: String
end

derive ToJson for Tag

struct TodoJson
    title: String
    tag: Tag
end

derive ToJson for TodoJson

pub fn encoded(title: String) -> String
    Json.encode_as(TodoJson{title:, tag: Tag{name: "home"}})
end
`

// derivesFromJson is api_test.nomi deriving FromJson for api.nomi's
// private TodoJson.
const derivesFromJson = `import api.{self, TodoJson}
import std::json.{FromJson, Json}

derive FromJson for TodoJson

test "decodes"
    todo_json = Json.decode_as<TodoJson>(api.encoded("milk"))!
    assert todo_json.title == "milk"
end
`

// apiProject writes files into a fresh directory and checks the file
// named entry, as nomi check does.
func apiProject(t *testing.T, entry string, files map[string]string) error {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Check(filepath.Join(dir, entry))
}

// withTagFromJson is apiSource whose Tag derives FromJson too, as a
// TodoJson decoded in the test needs.
var withTagFromJson = strings.Replace(strings.Replace(apiSource, "derive ToJson for Tag\n", "derive ToJson for Tag\nderive FromJson for Tag\n", 1),
	"import std::json.{Json, ToJson}", "import std::json.{FromJson, Json, ToJson}", 1)

// The partner test derives FromJson for its partner's private type, whose
// field names a type the test does not import.
func TestPartnerTestDerive_DerivesForThePartnersPrivateType(t *testing.T) {
	err := apiProject(t, "api_test.nomi", map[string]string{"api.nomi": withTagFromJson, "api_test.nomi": derivesFromJson})
	if err != nil {
		t.Fatalf("the partner test may not derive for its partner's type: %v", err)
	}
}

// The derived impl is the test file's: a program without the test file,
// whose production code decodes TodoJson, has no FromJson for it.
func TestPartnerTestDerive_ProductionCodeDoesNotSeeIt(t *testing.T) {
	api := withTagFromJson + `
pub fn title_of(text: String) -> String
    case Json.decode_as<TodoJson>(text)
        Ok(t) -> t.title
        Err(_) -> ""
    end
end
`
	files := map[string]string{
		"api.nomi":      api,
		"api_test.nomi": derivesFromJson,
		"main.nomi":     "import api\n\nfn main()\n    dbg api.title_of(api.encoded(\"milk\"))\nend\n",
	}
	if err := apiProject(t, "api_test.nomi", files); err != nil {
		t.Fatalf("the front end rejects the program with the test file, so this test shows nothing: %v", err)
	}
	err := apiProject(t, "main.nomi", files)
	if err == nil {
		t.Fatal("production code decodes TodoJson through an impl only the partner test derives")
	}
	if want := "TodoJson does not implement FromJson"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error does not say %q:\n%v", want, err)
	}
}

// A derive the partner already makes is a second impl, refused at the
// test's derive.
func TestPartnerTestDerive_DuplicatingThePartnersImplIsAnError(t *testing.T) {
	api := strings.Replace(withTagFromJson, "derive ToJson for TodoJson\n", "derive ToJson for TodoJson\nderive FromJson for TodoJson\n", 1)
	err := apiProject(t, "api_test.nomi", map[string]string{"api.nomi": api, "api_test.nomi": derivesFromJson})
	if err == nil {
		t.Fatal("the partner test derives FromJson a second time")
	}
	if want := "duplicate impl: `FromJson` for `TodoJson` is derived here and implemented in `api`"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error does not say %q:\n%v", want, err)
	}
}

// Only the partner test derives for another file's type: a test file named
// after no sibling, and an ordinary file, get the error a derive in another
// file always got.
func TestPartnerTestDerive_NoOtherFileDerivesForTheType(t *testing.T) {
	api := strings.Replace(withTagFromJson, "struct TodoJson", "pub struct TodoJson", 1)
	for _, entry := range []string{"other_test.nomi", "other.nomi"} {
		t.Run(entry, func(t *testing.T) {
			err := apiProject(t, entry, map[string]string{"api.nomi": api, entry: derivesFromJson})
			if err == nil {
				t.Fatalf("%s derives for api.nomi's type, though it is not its partner test", entry)
			}
			if want := "`derive ... for TodoJson` must be written in the file that declares `TodoJson`, or in that file's partner test file"; !strings.Contains(err.Error(), want) {
				t.Fatalf("error does not say %q:\n%v", want, err)
			}
		})
	}
}

// The interface name resolves through the test file's scope, as a derive's
// does in the declaring file.
func TestPartnerTestDerive_TheInterfaceMustBeInScope(t *testing.T) {
	test := strings.Replace(derivesFromJson, "import std::json.{FromJson, Json}", "import std::json.{Json}", 1)
	err := apiProject(t, "api_test.nomi", map[string]string{"api.nomi": withTagFromJson, "api_test.nomi": test})
	if err == nil {
		t.Fatal("the partner test derives an interface it does not import")
	}
	if want := "`FromJson` is not in scope — import `std::json.FromJson`"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error does not say %q:\n%v", want, err)
	}
}

// A field whose type lacks the interface is reported as for a derive in
// the declaring file, naming the derive and the field.
func TestPartnerTestDerive_AFieldWithoutTheImplIsAnError(t *testing.T) {
	err := apiProject(t, "api_test.nomi", map[string]string{"api.nomi": apiSource, "api_test.nomi": derivesFromJson})
	if err == nil {
		t.Fatal("the partner test derives FromJson for a type whose field has none")
	}
	if want := "no impl of `FromJson` for `Tag` (required via `derive FromJson for TodoJson`, field `tag`)"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error does not say %q:\n%v", want, err)
	}
	if strings.Contains(err.Error(), "$") {
		t.Fatalf("an error names a hidden import's alias:\n%v", err)
	}
}

// A hand-written impl in the partner test is admitted as a derive is, and
// for the same reason: the impl is the test file's.
func TestPartnerTestDerive_AHandWrittenImplToo(t *testing.T) {
	test := `import api.{TodoJson}

impl Display for TodoJson
    fn to_string(t: TodoJson) -> String
        "todo ${t.title}"
    end
end

test "displays"
    assert "${TodoJson{title: "milk", tag: api.Tag{name: "home"}}}" == "todo milk"
end
`
	test = strings.Replace(test, "import api.{TodoJson}", "import api.{self, TodoJson}", 1)
	if err := apiProject(t, "api_test.nomi", map[string]string{"api.nomi": apiSource, "api_test.nomi": test}); err != nil {
		t.Fatalf("the partner test may not implement an interface for its partner's type: %v", err)
	}
}
