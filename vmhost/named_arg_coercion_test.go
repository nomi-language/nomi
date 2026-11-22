package vmhost_test

import "testing"

// An operand of a qualified call written with named arguments becomes the
// interface value the checker recorded at it, as a positional one does:
// the call evaluates its operands first and passes each by a stand-in,
// which reads the written operand's coercion. An anonymous struct whose
// field is a container, passed as `ToJson` to std/http's `Response.json`
// beside `status:`, encodes through its fields' impls at their types,
// whether the field is a parameter, a call's result or a nested record,
// and so does the same call to std/json's `Json.encode_as(value:)` and to
// a program type's own function.
func TestNamedArgCoercion_AnAnonymousStructEntersToJson(t *testing.T) {
	got := runSourceOutput(t, `import std::http.{Response, Status}
import std::io
import std::json.{Json, ToJson}

struct Note
    title: String
    tags: List<String>
end

derive ToJson for Note

struct Reply
    text: String
end

impl Reply
    fn of(value: ToJson, status: Int = 200) -> Reply
        Reply{text: "${status} ${Json.encode(ToJson.to_json(value))}"}
    end
end

fn body(r: Response) -> String
    case Response.text(r)
        Ok(text) -> "${r.status}: ${text}"
        Err(_) -> "no body"
    end
end

fn problems() -> Map<String, List<String>>
    {"email" => ["is invalid"]}
end

fn by_parameter(
    m: Map<String, List<String>>,
    l: List<String>,
    v: Vector<Int>,
    s: Set<String>,
    maybe: Maybe<List<Int>>,
    nested: {inner: Map<String, List<String>>},
    note: Note,
)
    io.print(body(Response.json({errors: m}, status: Status.unprocessable_entity)))
    io.print(body(Response.json({errors: l}, status: Status.unprocessable_entity)))
    io.print(body(Response.json({errors: v}, status: Status.unprocessable_entity)))
    io.print(body(Response.json({errors: s}, status: Status.unprocessable_entity)))
    io.print(body(Response.json({errors: maybe}, status: Status.unprocessable_entity)))
    io.print(body(Response.json({errors: nested}, status: Status.unprocessable_entity)))
    io.print(body(Response.json({errors: note}, status: Status.unprocessable_entity)))
end

fn main()
    by_parameter(
        problems(),
        ["a"],
        Vector[1, 2],
        Set["s"],
        Some([3]),
        {inner: problems()},
        Note{title: "t", tags: ["x"]},
    )
    io.print(body(Response.json({errors: Map.put(problems(), "name", ["is blank"])}, status: Status.unprocessable_entity)))
    io.print(body(Response.json({errors: {inner: problems()}}, status: Status.unprocessable_entity)))
    io.print(body(Response.json(status: Status.created, value: {errors: problems()})))
    io.print(Json.encode_as(value: {errors: problems()}))
    io.print(Reply.of({errors: problems()}, status: 422).text)
end
`)
	want := `422 Unprocessable Entity: {"errors":{"email":["is invalid"]}}
422 Unprocessable Entity: {"errors":["a"]}
422 Unprocessable Entity: {"errors":[1,2]}
422 Unprocessable Entity: {"errors":["s"]}
422 Unprocessable Entity: {"errors":[3]}
422 Unprocessable Entity: {"errors":{"inner":{"email":["is invalid"]}}}
422 Unprocessable Entity: {"errors":{"title":"t","tags":["x"]}}
422 Unprocessable Entity: {"errors":{"email":["is invalid"],"name":["is blank"]}}
422 Unprocessable Entity: {"errors":{"inner":{"email":["is invalid"]}}}
201 Created: {"errors":{"email":["is invalid"]}}
{"errors":{"email":["is invalid"]}}
422 {"errors":{"email":["is invalid"]}}
`
	if got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
}
