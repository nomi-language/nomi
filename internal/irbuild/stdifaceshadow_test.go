package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `impl calendar.DateParts for Q` in a file that declares its own
// `interface DateParts` implements std's interface, named through its
// module, and its impls are filed apart from the local interface's, so both
// impls for Q are called. The impl used to resolve its interface by the bare
// name, the local one, and was refused.
func TestIRStdIface_AQualifiedImplBesideALocalInterfaceOfItsName(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::calendar
    std::io
end

interface DateParts
    fn year(value: self) -> Int
end

struct Q
    y: Int
end

impl DateParts for Q
    fn year(q: Q) -> Int
        q.y + 1000
    end
end

impl calendar.DateParts for Q
    fn year(q: Q) -> Int
        q.y
    end

    fn month(q: Q) -> Int
        q.y
    end

    fn day(q: Q) -> Int
        q.y
    end
end

fn main()
    io.print(Int.to_string(calendar.DateParts.year(Q{y: 7})))
    io.print(Int.to_string(DateParts.year(Q{y: 7})))
end
`, "7\n1007\n")
}

// A record's JSON in a file that declares its own `interface ToJson`, with
// impls of it beside std's, is std's: the builder's `ToJson.to_json_field`
// and `ToJson.to_json` calls on the fields name std's interface by their
// records, never by the spelling the file binds to its own, and the default
// `to_json_field` calls std's impl. The calls used to be resolved by the
// bare name, the file's interface, and were refused.
func TestIRStdIface_RecordJsonBesideALocalToJson(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::io
    std::json
    std::json.Json
end

interface ToJson
    fn to_json(value: self) -> Json
end

struct User
    name: String
end

impl json.ToJson for User
    fn to_json(u: User) -> Json
        Json.Obj{"name" => Json.String(u.name)}
    end
end

impl ToJson for User
    fn to_json(u: User) -> Json
        Json.String("local ${u.name}")
    end
end

impl ToJson for Int
    fn to_json(n: Int) -> Json
        Json.String("local ${n}")
    end
end

fn main()
    io.print(Json.encode_as({user: User{name: "a"}, n: 1}))
    io.print(Json.encode_as({n: 2, m: Some(3), user: Some(User{name: "e"})}))
    io.print(Json.encode_as(User{name: "d"}))
    io.print(Json.encode(json.ToJson.to_json(User{name: "b"})))
    io.print(Json.encode(ToJson.to_json(User{name: "c"})))
    io.print(Json.encode(ToJson.to_json(4)))
end
`, "{\"n\":1,\"user\":{\"name\":\"a\"}}\n{\"m\":3,\"n\":2,\"user\":{\"name\":\"e\"}}\n{\"name\":\"d\"}\n{\"name\":\"b\"}\n\"local c\"\n\"local 4\"\n")
}

// `derive json.ToJson` and `derive json.FromJson` in a file that declares
// its own ToJson and FromJson, with impls of them, derive std's: the
// derived bodies name std's interfaces through the module, never the
// file's spelling, and the file's own impls stay its own. A generic type
// derives the same way.
func TestIRStdIface_DeriveQualifiedBesideALocalToJson(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::io
    std::json
end

interface ToJson
    fn to_json(value: self) -> String
end

interface FromJson
    fn from_json(text: String) -> self
end

struct User
    name: String
    tag_list: List<String>
end

impl ToJson for User
    fn to_json(value: User) -> String
        "local ${value.name}"
    end
end

impl FromJson for User
    fn from_json(text: String) -> User
        User{name: "local ${text}", tag_list: []}
    end
end

derive json.ToJson for User with json.ToJson.Options{rename_all: json.Json.Case.Camel}
derive json.FromJson for User

struct Box<T>
    item: T
end

derive json.ToJson for Box
derive json.FromJson for Box

fn main()
    u = User{name: "ann", tag_list: ["a"]}
    io.print(ToJson.to_json(u))
    io.print(json.Json.encode_as(u))
    io.print(json.Json.encode_as(Box{item: u}))
    case json.Json.decode_as<Box<Int>>(`+"`"+`{"item":7}`+"`"+`)
        Ok(b)  -> io.print(b.item)
        Err(_) -> io.print("error")
    end

    io.print(FromJson.from_json<User>("cy").name)
end
`, "local ann\n{\"name\":\"ann\",\"tagList\":[\"a\"]}\n{\"item\":{\"name\":\"ann\",\"tagList\":[\"a\"]}}\n7\nlocal cy\n")
}

// A default of std's ToJson written through its module,
// `json.ToJson.to_json_field(s)`, in a file that declares its own
// `interface ToJson`, is std's default at the receiver. The route for a std
// interface named through its module found only impls that write the
// function, and refused the call.
func TestIRStdIface_AQualifiedDefaultBesideALocalInterfaceOfItsName(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::io
    std::json
end

interface ToJson
    fn to_json(value: self) -> String
end

fn main()
    f = json.ToJson.to_json_field("ann")
    io.print("${f}")
end
`, "Some(\"ann\")\n")
}

// A record decoded in a file that declares its own `interface FromJson`,
// with an impl of it beside std's for the field's type, reads the field by
// std's impl, which the builder's `F.from_json` call names by its record.
// Both impls declaring `from_json` for the type used to leave the call
// naming none, and it was refused.
func TestIRStdIface_RecordFromJsonBesideALocalFromJson(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::io
    std::json
    std::json.Json
end

interface FromJson
    fn from_json(text: String) -> self
end

struct User
    name: String
end

impl json.FromJson for User
    fn from_json(json: Json) -> Result<User, Json.ShapeError>
        case json
            Json.Obj(fields) ->
                case Map.get(fields, "name")
                    Some(Json.String(name)) -> Ok(User{name:})
                    _                       -> Ok(User{name: "?"})
                end

            _ ->
                Ok(User{name: "??"})
        end
    end
end

impl FromJson for User
    fn from_json(text: String) -> User
        User{name: "local ${text}"}
    end
end

fn main()
    case Json.decode_as<{user: User, n: Int}>(`+"`"+`{"user":{"name":"a"},"n":1}`+"`"+`)
        Ok(r)  -> io.print("${r.user.name} ${r.n}")
        Err(_) -> io.print("error")
    end

    io.print(FromJson.from_json<User>("b").name)
end
`, "a 1\nlocal b\n")
}

// An assertion subject's `failure` is std's Assertable impl in a file that
// declares its own `interface Assertable` with an impl for the type: the
// builder reads std's impls under the key the file files them under
// (stdIfaceImplKey). It used to take the file's own impl, whose
// `Option<String>` the VM refused.
func TestIRStdIface_AssertableBesideALocalAssertable(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::assertions
    std::assertions.AssertionDetails
    std::io
    std::testing
end

interface Assertable
    fn failure(value: self) -> Option<String>
end

struct Check
    ok: Bool
end

impl assertions.Assertable for Check
    fn failure(c: Check) -> Option<AssertionDetails>
        if c.ok then
            None
        else
            Some(AssertionDetails{reason: "std failure"})
        end
    end
end

impl Assertable for Check
    fn failure(c: Check) -> Option<String>
        if c.ok then Some("local failure") else None
    end
end

fn main()
    io.print(Result.is_ok(testing.check(Check{ok: True})))
    io.print(Result.is_ok(testing.check(Check{ok: False})))
    io.print(Assertable.failure(Check{ok: True}))
end
`, "True\nFalse\nSome(local failure)\n")
}

// Two files of one module, each declaring an interface named Show. A file
// that implements both for one type files the two impls under the two
// interfaces' keys (ifaceKey), so neither is refused as the other's
// duplicate and each use reaches the impl of the interface it names.
var twoShows = map[string]string{
	"a.nomi": `pub interface Show
    fn show(value: self) -> String

    fn label(value: self) -> String
        "A:${show(value)}"
    end
end
`,
	"b.nomi": `pub interface Show
    fn show(value: self) -> String

    fn label(value: self) -> String
        "B:${show(value)}"
    end
end
`,
}

// twoShowsUser is a file that implements both Shows for its User, then
// body.
func twoShowsUser(body string) string {
	return `import
    a
    b
    std::io
end

struct User
    name: String
end

impl a.Show for User
    fn show(value: User) -> String
        "a ${value.name}"
    end
end

impl b.Show for User
    fn show(value: User) -> String
        "b ${value.name}"
    end
end
` + body
}

func TestIRIfaceIdentity_QualifiedCallsOfTwoInterfacesOfOneName(t *testing.T) {
	verifyLambdaProgram(t, twoShowsUser(`
fn main()
    u = User{name: "ann"}
    io.print(a.Show.show(u))
    io.print(b.Show.show(u))
end
`), "a ann\nb ann\n", twoShows)
}

func TestIRIfaceIdentity_ValuesOfTwoInterfacesOfOneName(t *testing.T) {
	verifyLambdaProgram(t, twoShowsUser(`
fn ta(s: a.Show) -> String
    a.Show.show(s)
end

fn tb(s: b.Show) -> String
    b.Show.show(s)
end

fn main()
    u = User{name: "ann"}
    sa: a.Show = u
    sb: b.Show = u
    io.print(a.Show.show(sa))
    io.print(b.Show.show(sb))
    io.print(ta(u as a.Show))
    io.print(tb(u as b.Show))
end
`), "a ann\nb ann\na ann\nb ann\n", twoShows)
}

func TestIRIfaceIdentity_DefaultsOfTwoInterfacesOfOneName(t *testing.T) {
	verifyLambdaProgram(t, twoShowsUser(`
fn main()
    u = User{name: "ann"}
    io.print(a.Show.label(u))
    io.print(b.Show.label(u))
end
`), "A:a ann\nB:b ann\n", twoShows)
}

func TestIRIfaceIdentity_BoundsOfTwoInterfacesOfOneName(t *testing.T) {
	verifyLambdaProgram(t, twoShowsUser(`
fn ra<T>(x: T) -> String where T: a.Show
    a.Show.show(x)
end

fn rb<T>(x: T) -> String where T: b.Show
    b.Show.label(x)
end

fn main()
    u = User{name: "ann"}
    io.print(ra(u))
    io.print(rb(u))
end
`), "a ann\nB:b ann\n", twoShows)
}

// `T.show(x)` names the function of T's bound, so under `where T: b.Show`
// it calls b's impl although the type implements a's Show too.
func TestIRIfaceIdentity_ATypeParameterCallNamesItsBound(t *testing.T) {
	verifyLambdaProgram(t, twoShowsUser(`
fn ra<T>(x: T) -> String where T: a.Show
    T.show(x)
end

fn rb<T>(x: T) -> String where T: b.Show
    T.show(x)
end

fn main()
    u = User{name: "ann"}
    io.print(ra(u))
    io.print(rb(u))
end
`), "a ann\nb ann\n", twoShows)
}

// An impl header names a generic interface through its module beside this
// file's own interface of the name, and each call through a bound, the
// type and the interface reaches it.
func TestIRIfaceIdentity_AModuleQualifiedGenericHeader(t *testing.T) {
	scale := map[string]string{"scale_a.nomi": `pub interface Scale<F>
    fn scale(x: self, by: F) -> self
end
`}
	verifyLambdaProgram(t, `import
    scale_a
    std::io
end

interface Scale<F>
    fn scale(x: self, by: F) -> self
end

struct Money
    cents: Int
end

impl scale_a.Scale<Int> for Money
    fn scale(x: Money, by: Int) -> Money
        Money{cents: x.cents * by}
    end
end

fn twice<T>(x: T) -> T where T: scale_a.Scale<Int>
    T.scale(x, 2)
end

fn main()
    io.print(twice(Money{cents: 3}).cents)
    io.print(Money.scale(Money{cents: 5}, 2).cents)
end
`, "6\n10\n", scale)
}

// A type implementing both its file's FromJson and std's decodes as a
// field of a derived struct: std's `decode_field<User>` calls `T.from_json`,
// which goes through its bound, std's FromJson, and not the file's.
func TestIRIfaceIdentity_AStdInstanceCallsItsBoundsImpl(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::io
    std::json
end

interface FromJson
    fn from_json(text: String) -> Result<self, String>
end

struct User
    name: String
end

derive json.FromJson for User

impl FromJson for User
    fn from_json(text: String) -> Result<User, String>
        Ok(User{name: text})
    end
end

struct Team
    lead: User
    members: List<User>
end

derive json.FromJson for Team

fn main()
    team: Result<Team, json.Json.Error> = json.Json.decode_as(`+"`"+`{"lead": {"name": "ann"}, "members": [{"name": "bob"}]}`+"`"+`)
    io.print(team |> Result.map(fn t -> "${t.lead.name} ${List.length(t.members)}"))
    own: Result<User, String> = FromJson.from_json("cy")
    io.print(own |> Result.map(fn u -> u.name))
end
`, "Ok(ann 1)\nOk(cy)\n")
}

// Two impls of one interface declaration for one type are a duplicate,
// which the checker rejects before the builder sees the program.
func TestIRIfaceIdentity_TwoImplsOfOneInterfaceAreADuplicate(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"a.nomi": twoShows["a.nomi"], "main.nomi": `import
    a
    std::io
end

struct User
    name: String
end

impl a.Show for User
    fn show(value: User) -> String
        "one ${value.name}"
    end
end

impl a.Show for User
    fn show(value: User) -> String
        "two ${value.name}"
    end
end

fn main()
    io.print(a.Show.show(User{name: "ann"}))
end
`}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Analyze(filepath.Join(dir, "main.nomi"))
	if err == nil {
		t.Fatal("the front end admits two impls of one interface for one type")
	}
	if !strings.Contains(err.Error(), "duplicate impl: `a.Show` is implemented for `User`") {
		t.Fatalf("the front end rejects the duplicate with another error: %v", err)
	}
}
