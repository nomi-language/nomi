package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// A read of a subtype reads its base and checks the subtype's rules, in
// every reader: an anonymous struct's JSON, a derived FromJson, a std
// container's elements, a record read from texts by a listed std module
// (std::parse) and a route's parameters, a private subtype's and a generic
// one's included. The instance that reads Port is not the one at Int, so
// one program reads `{port: 0}` as both, each its own way.
func TestSubtypeRead_EachReaderChecksTheSubtype(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `import
    std::http.{Body, Method, Response, Route, Router, Status}
    std::http::testing
    std::io
    std::json.{FromJson, Json}
    std::parse
end

subtype Port Int when fn p -> p >= 1 and p <= 65535

subtype Many<A> List<A> when fn xs -> not Iter.is_empty(xs)

struct Server
    ports: Map<String, Port>
    hosts: Many<String>
end

derive FromJson for Server

fn shown<A>(r: Result<A, Json.Error>) -> String
    case r
        Ok(_)  -> "ok"
        Err(e) -> "${e}"
    end
end

fn main()
    io.print("${Json.decode_as<{port: Port}>(` + "`" + `{"port": 80}` + "`" + `) == Ok({port: 80})}")
    io.print(shown(Json.decode_as<{port: Port}>(` + "`" + `{"port": 0}` + "`" + `)))
    io.print("${Json.decode_as<{port: Int}>(` + "`" + `{"port": 0}` + "`" + `) == Ok({port: 0})}")
    io.print(shown(Json.decode_as<List<Port>>("[1, 0]")))
    io.print(shown(Json.decode_as<Server>(` + "`" + `{"ports": {"a": 1}, "hosts": ["h"]}` + "`" + `)))
    io.print(shown(Json.decode_as<Server>(` + "`" + `{"ports": {"a": 0}, "hosts": ["h"]}` + "`" + `)))
    io.print(shown(Json.decode_as<Server>(` + "`" + `{"ports": {"a": 1}, "hosts": []}` + "`" + `)))
    io.print("${parse.read_fields<{v: Port}>({"v" => "80"}) == Some({v: 80})}")
    io.print("${parse.read_fields<{v: Port}>({"v" => "0"}) == None}")
    io.print("${parse.read_fields<{v: Int}>({"v" => "0"}) == Some({v: 0})}")
    item: Route<{id: Port}> = "GET /items/{id}"
    router = Router.new([Route.to_with(item, fn _, p -> Response{body: Body.Text("item ${p.id}")})])!
    io.print("${Router.serve(router, testing.request(Method.Get, "/items/7")) |> Response.text() == Ok("item 7")}")
    io.print("${Router.serve(router, testing.request(Method.Get, "/items/0")).status == Status.not_found}")
end
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	p.Declines.Observed = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.IR {
		if m.Name() != path {
			continue
		}
		var out bytes.Buffer
		if _, err := vm.NewProgram(m, res.IRModules(), &out).Run("main"); err != nil {
			t.Fatalf("the VM could not run it: %v (declines: %v)", err, declined)
		}
		want := "True\n" +
			"port: expected Port, got 0\n" +
			"True\n" +
			"[1]: expected Port, got 0\n" +
			"ok\n" +
			"ports.a: expected Port, got 0\n" +
			"hosts: expected Many<String>, got []\n" +
			"True\nTrue\nTrue\nTrue\nTrue\n"
		if out.String() != want {
			t.Fatalf("VM:\n%s\nwant:\n%s", out.String(), want)
		}
		return
	}
	t.Fatalf("the fixture retained nothing (declines: %v)", declined)
}
