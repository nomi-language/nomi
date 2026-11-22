package vmrunner_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
	"github.com/nomi-language/nomi/vmrunner"
)

const hostCallsProgram = `import std::json.{FromJson, ToJson}

struct Point
    x: Int
    y: Int
end

derive ToJson for Point
derive FromJson for Point

host fn add(a: Int, b: Int) -> Int

host fn mirror(p: Point) -> Point

host fn note(text: String)

export fn sum(a: Int, b: Int) -> Int
    add(a, b) + 1
end

export fn flip(x: Int, y: Int) -> Int
    p = mirror(Point{x:, y:})
    p.x * 10 + p.y
end

export fn told(text: String) -> String
    note(text)
    "noted"
end

host fn lookup(id: Int) -> Result<String, String>

export fn find(id: Int) -> String
    case lookup(id)
        Ok(name) -> name
        Err(why) -> "not found: ${why}"
    end
end
`

// hostCallsCallback adds to hostCallsProgram a `host fn` that takes a
// function value, which has no JSON form.
const hostCallsCallback = hostCallsProgram + `
host fn apply(n: Int, f: (Int) -> Int) -> Int

export fn twice(n: Int) -> Int
    apply(n, fn x -> x * 2)
end
`

// hostCallsImage lowers src as `nomi build --no-vm` does, admitting its
// `host fn`s for the host that loads it, and answers the image.
func hostCallsImage(t *testing.T, src string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := vmhost.Load(path, vmhost.WithLoaderHosts())
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := p.BuildExportsImage()
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// jsonHost answers add, mirror and note as a JSON host does: from the
// arguments' JSON, in the envelope nomi.host_call answers. keys records each
// key it was asked for and args each argument list's text.
type jsonHost struct {
	keys, args []string
}

func (h *jsonHost) call(key string, args []byte) ([]byte, error) {
	h.keys = append(h.keys, key)
	h.args = append(h.args, string(args))
	var in []json.RawMessage
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	switch key {
	case "add":
		var a, b int
		_ = json.Unmarshal(in[0], &a)
		_ = json.Unmarshal(in[1], &b)
		return json.Marshal(map[string]int{"ok": a + b})
	case "mirror":
		var p struct{ X, Y int }
		_ = json.Unmarshal(in[0], &p)
		return []byte(`{"ok": {"x": ` + itoa(p.Y) + `, "y": ` + itoa(p.X) + `}}`), nil
	case "note":
		var text string
		_ = json.Unmarshal(in[0], &text)
		switch text {
		case "boom":
			return []byte(`{"error": "the host refused boom"}`), nil
		case "garbled":
			return []byte(`not json`), nil
		}
		return []byte(`{"ok": null}`), nil
	case "lookup":
		var id int
		_ = json.Unmarshal(in[0], &id)
		switch id {
		case 1:
			return []byte(`{"ok": {"Ok": "ada"}}`), nil
		case 2:
			return []byte(`{"ok": {"Err": "no user 2"}}`), nil
		}
		return []byte(`{"ok": {"ok": "ada"}}`), nil
	}
	return []byte(`{"error": "no such host function"}`), nil
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// A JSON host answers a program's `host fn`s by key: Ints, a struct both
// ways, a Unit result and a Result's two arms inside the envelope, each
// argument encoded by its type's ToJson. An `{"error": ...}` answer fails
// the call with the host's message, an answer in no envelope fails it
// saying so, a Result in a shape FromJson refuses fails it naming the
// shape, and the program takes the next call.
func TestHostCalls_AJSONHostAnswersAProgramsHostFunctions(t *testing.T) {
	img := hostCallsImage(t, hostCallsProgram)
	h := &jsonHost{}
	in, err := vmrunner.Boot(img, io.Discard, io.Discard, nil, nil,
		vmrunner.WithHostCalls("the test's keys", []string{"add", "mirror", "note", "lookup"}, h.call))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, c := range []struct{ name, args, want string }{
		{"sum", `[2, 3]`, `6`},
		{"flip", `[1, 2]`, `21`},
		{"told", `["fine"]`, `"noted"`},
		{"find", `[1]`, `"ada"`},
		{"find", `[2]`, `"not found: no user 2"`},
	} {
		got, err := in.Call(ctx, c.name, []byte(c.args))
		if err != nil || string(got) != c.want {
			t.Errorf("%s(%s) = %s, %v; want %s", c.name, c.args, got, err, c.want)
		}
	}
	if want := []string{"[2,3]", `[{"x":1,"y":2}]`, `["fine"]`, "[1]", "[2]"}; strings.Join(h.args, " ") != strings.Join(want, " ") {
		t.Errorf("the host was handed %q, want %q", h.args, want)
	}
	if _, err := in.Call(ctx, "told", []byte(`["boom"]`)); err == nil || err.Error() != "line 27: the host refused boom" {
		t.Errorf("told(boom) failed with %v, want the host's message at the call's line", err)
	}
	_, err = in.Call(ctx, "told", []byte(`["garbled"]`))
	if err == nil || !strings.Contains(err.Error(), "host function `note` answered \"not json\", which is neither {\"ok\": <result>} nor {\"error\": \"<message>\"}") {
		t.Errorf("told(garbled) failed with %v, want the answer named", err)
	}
	_, err = in.Call(ctx, "find", []byte(`[3]`))
	const lowercase = "host function `lookup` answered {\"ok\": \"ada\"}, which is not a Result<String, String>: " +
		`expected Result as {"Ok": value} or {"Err": error}, got object with keys "ok"`
	if err == nil || !strings.Contains(err.Error(), lowercase) {
		t.Errorf("find(3) failed with %v, want the Result's shape named", err)
	}
	if got, err := in.Call(ctx, "sum", []byte(`[4, 5]`)); err != nil || string(got) != "10" {
		t.Errorf("sum(4, 5) after the failures = %s, %v; want 10", got, err)
	}
}

// The load refuses, naming the key, a `host fn` no table answers and the
// host does not list, and a listed one whose signature has no JSON form.
func TestHostCalls_TheLoadRefusesWhatTheHostCannotAnswer(t *testing.T) {
	img := hostCallsImage(t, hostCallsCallback)
	h := &jsonHost{}
	_, err := vmrunner.Boot(img, io.Discard, io.Discard, nil, nil,
		vmrunner.WithHostCalls("the test's keys", []string{"add", "mirror", "note", "lookup"}, h.call))
	if err == nil {
		t.Fatal("the load admits a program whose `host fn apply` nothing answers")
	}
	const unlisted = "no host table of this runner answers apply, a `host fn` the program declares; list its key in the test's keys"
	if err.Error() != unlisted {
		t.Errorf("the load failed with\n%v\nwant\n%s", err, unlisted)
	}
	_, err = vmrunner.Boot(img, io.Discard, io.Discard, nil, nil,
		vmrunner.WithHostCalls("the test's keys", []string{"add", "mirror", "note", "lookup", "apply"}, h.call))
	if err == nil {
		t.Fatal("the load admits a `host fn` that takes a function value for a JSON host")
	}
	const noForm = "the test's keys lists \"apply\", and parameter `f` of host function `apply` cannot cross as JSON: " +
		"`(Int) -> Int` does not implement `ToJson` (`host fn apply(n: Int, f: (Int) -> Int) -> Int`)"
	if !strings.HasPrefix(err.Error(), noForm) {
		t.Errorf("the load failed with\n%v\nwant it to start\n%s", err, noForm)
	}
	if len(h.keys) != 0 {
		t.Errorf("a refused load called the host for %v", h.keys)
	}
}
