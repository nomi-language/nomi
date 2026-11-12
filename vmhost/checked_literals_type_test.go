package vmhost

import (
	"context"
	"strings"
	"testing"
)

// patTag is a checked literal over a generic type whose check_type refuses
// every type argument but String, naming what it got.
const patTag = `import
    std::io
    std::literals.{CheckedLiteral, Fragment, LiteralError}
end

pub opaque type Pat<P> String

impl CheckedLiteral<String, Pat<P>> for Pat<P>
    fn check(statics: List<String>) -> Result<Unit, LiteralError>
        if statics == ["bad"] then
            Err(LiteralError{fragment: 0, offset: 0, message: "bad pattern"})
        else
            Ok(Unit)
        end
    end

    fn check_type(statics: List<String>, arguments: List<String>) -> Result<Unit, LiteralError>
        check(statics)?

        if arguments == ["String"] then
            Ok(Unit)
        else
            Err(LiteralError{fragment: 0, offset: 1, message: "got ${arguments}"})
        end
    end

    fn build(fragments: List<Fragment<String>>) -> Pat<P>
        text = Iter.reduce(fragments, "", fn acc, frag ->
            case frag
                .Static(s) -> acc + s
                .Dynamic(v) -> acc + v
            end
        end)

        Pat(text)
    end
end

fn text_of<P>(p: Pat<P>) -> String
    Pat(t) = p
    t
end

`

// A checked literal of a generic type gets its own type arguments, which
// the expected type gives, and its check_type gets each one's spelling.
// check_type runs in check's place, and a type that does not write it is
// checked by check alone.
func TestCheckedLiteral_CheckTypeSeesTheTypeArguments(t *testing.T) {
	src := patTag + "fn main()\n    a: Pat<String> = \"hello\"\n    b: Pat<String> = Pat`there`\n    io.print(text_of(a))\n    io.print(text_of(b))\nend\n"
	var out strings.Builder
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello\nthere\n" {
		t.Errorf("output = %q", out.String())
	}
	for _, c := range []struct{ line, message string }{
		{"    a: Pat<Int> = \"nope\"", "`Pat` literal \"nope\" is invalid: got [Int]"},
		{"    a: Pat<{id: Maybe<Int>, name: String}> = \"nope\"", "`Pat` literal \"nope\" is invalid: got [{id: Maybe<Int>, name: String}]"},
		{"    a: Pat<String> = \"bad\"", "`Pat` literal \"bad\" is invalid: bad pattern"},
		{"    a: Pat<Int> = Pat`nope`", "typed literal Pat`nope` is invalid: got [Int]"},
	} {
		src := patTag + "fn main()\n" + c.line + "\n    io.print(text_of(a))\nend\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 || ds[0].Message != c.message {
			t.Errorf("%s: %v, want one error %q", c.line, ds, c.message)
		}
	}
}
