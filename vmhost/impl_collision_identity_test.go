package vmhost_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// An API's error type converts from two libraries' errors that are each
// called `Error`: `impl From<a.Error> for Failure` beside `impl From<b.Error>
// for Failure` are two impls, not a duplicate, and `From.from`, the type's
// own `Failure.from`, a `?` and a `where T: From<b.Error>` bound each reach
// the impl for the value's own `Error`.
func TestImplCollision_SameNamedTypeArgumentsRunTheirOwnImpls(t *testing.T) {
	got := runModule(t, map[string]string{
		"a.nomi": "pub struct Error\n    text: String\nend\n",
		"b.nomi": "pub struct Error\n    code: Int\nend\n",
		"main.nomi": `import
    std::io
    a
    b
end

enum Failure
    A a.Error
    B b.Error
end

impl From<a.Error> for Failure
    fn from(e: a.Error) -> Failure
        Failure.A(e)
    end
end

impl From<b.Error> for Failure
    fn from(e: b.Error) -> Failure
        Failure.B(e)
    end
end

fn fail_a() -> Result<Int, a.Error>
    Err(a.Error{text: "x"})
end

fn fail_b() -> Result<Int, b.Error>
    Err(b.Error{code: 7})
end

fn via(first: Bool) -> Result<Int, Failure>
    n = if first then fail_a()? else fail_b()?
    Ok(n)
end

fn convert<T>(e: b.Error) -> T where T: From<b.Error>
    T.from(e)
end

fn name(f: Failure) -> String
    case f
        .A(e) -> "a ${e.text}"
        .B(e) -> "b ${e.code}"
    end
end

fn shown(r: Result<Int, Failure>) -> String
    case r
        Ok(_) -> "ok"
        Err(f) -> name(f)
    end
end

fn main()
    io.print(name(From.from(a.Error{text: "one"})))
    io.print(name(From.from(b.Error{code: 2})))
    io.print(name(Failure.from(a.Error{text: "three"})))
    io.print(name(Failure.from(b.Error{code: 4})))
    io.print(shown(via(True)))
    io.print(shown(via(False)))
    f: Failure = convert(b.Error{code: 5})
    io.print(name(f))
end
`,
	})
	if want := "a one\nb 2\na three\nb 4\na x\nb 7\nb 5\n"; got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}

// Two dependencies of a project, each declaring `Error` in its own
// `errors.nomi`, and a module of the project converting from both.
func TestImplCollision_TwoDependenciesErrorsConvertApart(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"sqlish/nomi.toml":    "[package]\nname = \"sqlish\"\n",
		"sqlish/errors.nomi":  "pub struct Error\n    text: String\nend\n",
		"httpish/nomi.toml":   "[package]\nname = \"httpish\"\n",
		"httpish/errors.nomi": "pub struct Error\n    code: Int\nend\n",
		"app/nomi.toml": `[package]
name = "app"
entry_points = ["main"]

[dependencies]
sqlish = { dir = "../sqlish" }
httpish = { dir = "../httpish" }
`,
		"app/api/errors.nomi": `import
    sqlish
    httpish
end

pub enum ApiError
    Db sqlish::errors.Error
    Web httpish::errors.Error
end

impl From<sqlish::errors.Error> for ApiError
    fn from(e: sqlish::errors.Error) -> ApiError
        ApiError.Db(e)
    end
end

impl From<httpish::errors.Error> for ApiError
    fn from(e: httpish::errors.Error) -> ApiError
        ApiError.Web(e)
    end
end

pub fn name(e: ApiError) -> String
    case e
        .Db(d) -> "db ${d.text}"
        .Web(w) -> "web ${w.code}"
    end
end
`,
		"app/main.nomi": `import
    std::io
    sqlish
    httpish
    app::api::errors.{self, ApiError}
end

fn query() -> Result<Int, sqlish::errors.Error>
    Err(sqlish::errors.Error{text: "gone"})
end

fn fetch() -> Result<Int, httpish::errors.Error>
    Err(httpish::errors.Error{code: 404})
end

fn handler(db: Bool) -> Result<Int, ApiError>
    n = if db then query()? else fetch()?
    Ok(n)
end

fn shown(r: Result<Int, ApiError>) -> String
    case r
        Ok(_) -> "ok"
        Err(e) -> errors.name(e)
    end
end

fn main()
    io.print(shown(handler(True)))
    io.print(shown(handler(False)))
end
`,
	})
	p, err := vmhost.Load(filepath.Join(dir, "app", "main.nomi"))
	if err != nil {
		t.Fatalf("the front end refuses this program: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if want := "db gone\nweb 404\n"; out.String() != want {
		t.Errorf("printed %q, want %q", out.String(), want)
	}
}

// `impl From<Circle>` beside `impl From<Shape>`, where Shape embeds Circle,
// are two impls at a call as they are to coherence. Each call form, a `?` and
// a `where T: From<Shape>` bound run the impl at the value's own type, and a
// Circle, which fits both, runs From<Circle>. A Circle where only the Shape
// impl is written converts into it. `From.from(s)` was refused when the
// program was built, since the two impls were one candidate.
func TestImplCollision_AnEmbeddedTypeAndItsEnumRunTheirOwnImpls(t *testing.T) {
	got := runModule(t, map[string]string{
		"main.nomi": `import
    std::io
end

struct Circle
    radius: Int
end

enum Shape
    embeds Circle
    Point
end

enum Failure
    FromCircle Circle
    FromShape Shape
end

impl From<Circle> for Failure
    fn from(e: Circle) -> Failure
        Failure.FromCircle(e)
    end
end

impl From<Shape> for Failure
    fn from(e: Shape) -> Failure
        Failure.FromShape(e)
    end
end

enum Wide
    FromShape Shape
end

impl From<Shape> for Wide
    fn from(e: Shape) -> Wide
        Wide.FromShape(e)
    end
end

fn name(f: Failure) -> String
    case f
        .FromCircle(_) -> "circle"
        .FromShape(_) -> "shape"
    end
end

fn shown(r: Result<Int, Failure>) -> String
    case r
        Ok(_) -> "ok"
        Err(f) -> name(f)
    end
end

fn fail_circle() -> Result<Int, Circle>
    Err(Circle{radius: 2})
end

fn fail_shape() -> Result<Int, Shape>
    Err(Shape.Point)
end

fn via_circle() -> Result<Int, Failure>
    n = fail_circle()?
    Ok(n)
end

fn via_shape() -> Result<Int, Failure>
    n = fail_shape()?
    Ok(n)
end

fn widened() -> Result<Int, Wide>
    n = fail_circle()?
    Ok(n)
end

fn convert<T>(s: Shape) -> T where T: From<Shape>
    T.from(s)
end

fn main()
    c = Circle{radius: 1}
    s: Shape = Shape.Point
    io.print(name(From.from(c)))
    io.print(name(From.from(s)))
    io.print(name(Failure.from(c)))
    io.print(name(Failure.from(s)))
    f1: Failure = c |> From.from()
    io.print(name(f1))
    io.print(name(s |> Failure.from()))
    io.print(shown(via_circle()))
    io.print(shown(via_shape()))
    f2: Failure = convert(s)
    io.print(name(f2))
    w = case widened()
        Ok(_) -> "ok"
        Err(.FromShape(_)) -> "wide"
    end
    io.print(w)
end
`,
	})
	if want := "circle\nshape\ncircle\nshape\ncircle\nshape\ncircle\nshape\nshape\nwide\n"; got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}
