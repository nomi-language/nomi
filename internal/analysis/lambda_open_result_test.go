package analysis_test

import (
	"strings"
	"testing"
)

// The cases of spec §7 *Lambdas* on a lambda's open result type.

// A lambda handler answering only Ok(...) checks against Route.to_with's
// `(Request, P) -> ToResponse`: its error type is Infallible, and
// Result<Response, Infallible> is a ToResponse.
func TestLambdaOpenResult_OkOnlyHandlerChecks(t *testing.T) {
	src := `import std::http.{Body, Response, Route, Router, Status}

fn page(params: #{id: Int}) -> Response {
  Response{body: Body.Text("edit ${params.id}")}
}

fn main() {
  admin_post_edit: Route<#{id: Int}> = "POST /admin/posts/{id}/edit"
  _router = Router.new([
    Route.to_with(admin_post_edit, fn _request, params -> Ok(page(params))),
    Route.to("GET /gone", fn _ -> Err(Response.error(Status.not_found, "gone"))),
  ])
}
`
	expectClean(t, checkWithStdlib(src))
}

// Against a result that names the error type, the body's Ok(...) takes it:
// the lambda's result is Result<Int, MyError>, which a later Err match
// reads as one.
func TestLambdaOpenResult_ExpectedResultNamesTheError(t *testing.T) {
	src := `enum MyError {
  Bad
}

fn apply(f: (Int) -> Result<Int, MyError>, x: Int) -> Result<Int, MyError> {
  f(x)
}

fn main() {
  _r = case apply(fn x -> Ok(x + 1), 1) {
    Ok(n) -> n
    Err(.Bad) -> 0
  }
}
`
	expectClean(t, checkWithStdlib(src))
}

// Against an interface result, a body that also returns Err(e) names the
// error type from it, so the Err's own type must meet the interface: an Int
// error is no ToResponse.
func TestLambdaOpenResult_ErrNamesTheErrorType(t *testing.T) {
	src := `import std::http.{Body, Response, Route}

fn main() {
  _entry = Route.to("GET /n", fn r -> {
    if r.path == "/n" {
      Ok(Response{body: Body.Text("n")})
    } else {
      Err(404)
    }
  })
}
`
	errs := checkWithStdlib(src)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "Result<Response, Int>") {
			found = true
		}
	}
	if !found {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = diagText(e)
		}
		t.Fatalf("want an error naming Result<Response, Int>, got:\n%s", strings.Join(msgs, "\n---\n"))
	}
}

// With no expected function type the error type stays open, and the first
// bound it must meet says so and writes the annotation.
func TestLambdaOpenResult_NoExpectedTypeIsAnError(t *testing.T) {
	src := `import std::io

fn main() {
  f = fn x: Int -> Ok(x)
  io.print(f(1))
}
`
	expectOneDiagnostic(t, src,
		"Result<Int, _> needs Display (required by `where T: Display`), and nothing names the type its `_` stands for\n"+
			"help: annotate the binding this value comes from with its whole type, as in `r: Result<Int, String> = Ok(1)`, or a lambda's parameters and result, as in `f: (Int) -> Result<Int, String> = fn x -> Ok(x)`")
}

// The annotation that help writes makes the program check.
func TestLambdaOpenResult_TheAnnotationChecks(t *testing.T) {
	src := `import std::io

fn main() {
  f: (Int) -> Result<Int, String> = fn x -> Ok(x)
  io.print(f(1))
}
`
	expectClean(t, checkWithStdlib(src))
}
