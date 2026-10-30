package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// wantOneTypeParamError fails unless errs holds exactly one error, with
// message msg at line:col and the help hint.
func wantOneTypeParamError(t *testing.T, errs []analysis.TypeError, line, col int, msg, hint string) {
	t.Helper()
	if len(errs) == 0 {
		t.Fatalf("the front end admits this; want %d:%d %s", line, col, msg)
	}
	if len(errs) != 1 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("want one error, got %d:\n  %s", len(errs), strings.Join(msgs, "\n  "))
	}
	e := errs[0]
	if e.Line != line || e.Col != col || e.Message != msg {
		t.Fatalf("got %d:%d %s\nwant %d:%d %s", e.Line, e.Col, e.Message, line, col, msg)
	}
	if len(e.Hints) != 1 || e.Hints[0] != hint {
		t.Fatalf("hints = %q, want [%q]", e.Hints, hint)
	}
}

// A type parameter in an impl block's function is held to the same naming
// rule as a file-level function's. This is the fuzzer's program: the
// function's own `t` is unused, and its `T` is the impl's.
func TestFunctionTypeParams_LowercaseInImplIsRejected(t *testing.T) {
	src := `struct Span<T> {
    start: T
    stop: T
}

impl Span<T> {
    fn is_longer<t>(span: Span<T>, other: Span<T>) -> Bool where T: Comparable {
        span.stop > other.stop
    }
}
`
	_, errs := checkSourceWithStdlib(src)
	wantOneTypeParamError(t, errs, 7, 18,
		`type parameter "t" must be PascalCase`,
		"a type parameter is a type name, so it starts with an uppercase letter, such as `T`")
}

func TestFunctionTypeParams_LowercaseOnHostFnIsRejected(t *testing.T) {
	src := `host fn pick<e>(x: Int) -> Int
`
	_, errs := checkSourceWithStdlib(src)
	wantOneTypeParamError(t, errs, 1, 14,
		`type parameter "e" must be PascalCase`,
		"a type parameter is a type name, so it starts with an uppercase letter, such as `T`")
}

func TestFunctionTypeParams_UnusedInFileFunctionIsRejected(t *testing.T) {
	src := `fn unused<U>(x: Int) -> Int {
    x
}
`
	_, errs := checkSourceWithStdlib(src)
	wantOneTypeParamError(t, errs, 1, 11,
		"type parameter `U` is not used in the signature of `unused`, so no call can determine it",
		"remove `U` from the type parameters, or use it in a parameter's type or the return type")
}

// The impl's `T` is in the signature; the function's own `U` is not.
func TestFunctionTypeParams_UnusedInImplFunctionIsRejected(t *testing.T) {
	src := `struct Box<T> {
    v: T
}

impl Box<T> {
    fn odd<U>(b: Box<T>) -> T {
        b.v
    }
}
`
	_, errs := checkSourceWithStdlib(src)
	wantOneTypeParamError(t, errs, 6, 12,
		"type parameter `U` is not used in the signature of `odd`, so no call can determine it",
		"remove `U` from the type parameters, or use it in a parameter's type or the return type")
}

// A `where` bound alone does not determine a type parameter.
func TestFunctionTypeParams_OnlyBoundedIsRejected(t *testing.T) {
	src := `fn shown<T>(x: Int) -> Int where T: Display {
    x
}
`
	_, errs := checkSourceWithStdlib(src)
	wantOneTypeParamError(t, errs, 1, 10,
		"type parameter `T` is not used in the signature of `shown`, so no call can determine it",
		"remove `T` from the type parameters, or use it in a parameter's type or the return type")
}

func TestFunctionTypeParams_UnusedInInterfaceFunctionIsRejected(t *testing.T) {
	src := `interface Sized {
    fn size(value: self) -> Int

    fn twice<K>(value: self) -> Int {
        size(value) * 2
    }
}
`
	_, errs := checkSourceWithStdlib(src)
	wantOneTypeParamError(t, errs, 4, 14,
		"type parameter `K` is not used in the signature of `twice`, so no call can determine it",
		"remove `K` from the type parameters, or use it in a parameter's type or the return type")
}

func TestFunctionTypeParams_UnusedInNestedFunctionIsRejected(t *testing.T) {
	src := `fn outer(x: Int) -> Int {
    fn inner<Q>(y: Int) -> Int {
        y
    }
    inner(x)
}
`
	_, errs := checkSourceWithStdlib(src)
	wantOneTypeParamError(t, errs, 2, 14,
		"type parameter `Q` is not used in the signature of `inner`, so no call can determine it",
		"remove `Q` from the type parameters, or use it in a parameter's type or the return type")
}

// The forms that stay legal: a parameter only a bound projects, a parameter
// only the return type names, an impl function that redeclares the impl's
// own `T`, and one that adds its own parameter beside the impl's.
func TestFunctionTypeParams_DeterminedParametersAreAccepted(t *testing.T) {
	src := `struct Span<T> {
    start: T
    stop: T
}

impl Span<T> {
    fn first<T>(span: Span<T>) -> T {
        span.start
    }

    fn pair<U>(span: Span<T>, u: U) -> #(T, U) {
        #(span.start, u)
    }

    fn is_longer(span: Span<T>, other: Span<T>) -> Bool where T: Comparable {
        span.stop > other.stop
    }
}

fn has_any_items<I, T>(items: I) -> Bool where I: Iter<T> {
    Iter.count(items) > 0
}

fn none<T>() -> Maybe<T> {
    None
}
`
	_, errs := checkSourceWithStdlib(src)
	expectNoErrorsT(t, errs)
}
