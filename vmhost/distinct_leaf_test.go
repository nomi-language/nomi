package vmhost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A distinct over an rt leaf (Decimal, Byte, Bytes, a host handle such as
// Regex), over Unit, over a distinct over one of those, or over a function
// runs every way a distinct over Int does: the call form prefix and piped, a
// destructuring binding, parameter and lambda parameter, a `case` arm, the
// type name as a function value, an `embeds` variant, a struct field, Debug,
// and the derives the inner type admits. `nomi run` refused each of these
// with "using `Money` here is not supported yet" before: the builder admitted
// a distinct only over a scalar, a container, a tuple, a function or a
// declared type, so `Money(1.5d)` declined as "a type name in value
// position".
func TestDistinctOverLeaf_RunsEveryShape(t *testing.T) {
	const byteHelper = `
fn byte(n: Int) -> Byte {
    case Byte.from_int(n) {
        Some(b) -> b
        None -> byte(0)
    }
}
`
	cases := []struct {
		name, inner, imports, decls, v1, v2 string
		derives                             []string
		want                                string
	}{
		{
			name: "Decimal", inner: "Decimal", v1: "1.5d", v2: "2.5d",
			derives: []string{"Equatable", "Hashable", "Comparable", "Display"},
			want: `1.5d
2.5d
1.5d
W(1.5d)
[W(1.5d), W(2.5d)]
[1.5d, 2.5d]
W(1.5d)
2.5d
#S{w: W(1.5d)}
False True
Some(2)
True False
True False
1.5
`,
		},
		{
			name: "Byte", inner: "Byte", decls: byteHelper, v1: "byte(1)", v2: "byte(2)",
			derives: []string{"Equatable", "Hashable", "Display"},
			want: `1
2
1
W(1)
[W(1), W(2)]
[1, 2]
W(1)
2
#S{w: W(1)}
False True
Some(2)
True False
1
`,
		},
		{
			name: "Bytes", inner: "Bytes", v1: `String.to_bytes("a")`, v2: `String.to_bytes("bc")`,
			derives: []string{"Equatable", "Hashable", "Display"},
			want: `<<97>>
<<98, 99>>
<<97>>
W(<<97>>)
[W(<<97>>), W(<<98, 99>>)]
[<<97>>, <<98, 99>>]
W(<<97>>)
<<98, 99>>
#S{w: W(<<97>>)}
False True
Some(2)
True False
Bytes(1)
`,
		},
		{
			name: "Regex", inner: "Regex", imports: "import std/regex.Regex\n", v1: "Regex`a+`", v2: "Regex`b`",
			derives: []string{"Display"},
			want: "Regex`a+`\nRegex`b`\nRegex`a+`\nW(Regex`a+`)\n[W(Regex`a+`), W(Regex`b`)]\n[Regex`a+`, Regex`b`]\n" +
				"W(Regex`a+`)\nRegex`b`\n#S{w: W(Regex`a+`)}\na+\n",
		},
		{
			name: "Unit", inner: "Unit", v1: "Unit", v2: "Unit",
			want: `Unit
Unit
Unit
W(Unit)
[W(Unit), W(Unit)]
[Unit, Unit]
W(Unit)
Unit
#S{w: W(Unit)}
`,
		},
		{
			name: "a distinct over Decimal", inner: "Money", v1: "Money(1.5d)", v2: "Money(2.5d)",
			decls: `
type Money Decimal

derive Equatable for Money
derive Hashable for Money
derive Comparable for Money
derive Display for Money
`,
			derives: []string{"Equatable", "Hashable", "Comparable", "Display"},
			want: `Money(1.5d)
Money(2.5d)
Money(1.5d)
W(Money(1.5d))
[W(Money(1.5d)), W(Money(2.5d))]
[Money(1.5d), Money(2.5d)]
W(Money(1.5d))
Money(2.5d)
#S{w: W(Money(1.5d))}
False True
Some(2)
True False
True False
Money(1.5)
`,
		},
		{
			name: "a distinct over Bytes", inner: "Payload", v1: `Payload(String.to_bytes("a"))`, v2: `Payload(String.to_bytes("b"))`,
			decls: `
type Payload Bytes

derive Equatable for Payload
derive Hashable for Payload
`,
			derives: []string{"Equatable", "Hashable"},
			want: `Payload(<<97>>)
Payload(<<98>>)
Payload(<<97>>)
W(Payload(<<97>>))
[W(Payload(<<97>>)), W(Payload(<<98>>))]
[Payload(<<97>>), Payload(<<98>>)]
W(Payload(<<97>>))
Payload(<<98>>)
#S{w: W(Payload(<<97>>))}
False True
Some(2)
True False
`,
		},
		{
			name: "an empty struct", inner: "Empty", v1: "#Empty{}", v2: "#Empty{}",
			decls: `
struct Empty {
}
`,
			want: `#Empty{}
#Empty{}
#Empty{}
W(#Empty{})
[W(#Empty{}), W(#Empty{})]
[#Empty{}, #Empty{}]
W(#Empty{})
#Empty{}
#S{w: W(#Empty{})}
`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var src strings.Builder
			src.WriteString("import std/io\n" + c.imports + c.decls + "\ntype W " + c.inner + "\n\n")
			for _, d := range c.derives {
				src.WriteString("derive " + d + " for W\n")
			}
			src.WriteString(`
enum Holder {
    embeds W
    Other
}

struct S {
    w: W
}

fn unwrap(W(x)) -> ` + c.inner + ` {
    x
}

fn main() {
    a = W(` + c.v1 + `)
    b = ` + c.v2 + ` |> W()
    W(m) = a
    io.inspect(m)
    io.inspect(unwrap(b))
    io.inspect(
        case a {
            W(y) -> y
        }
    )
    io.inspect(a)
    io.inspect([` + c.v1 + `, ` + c.v2 + `] |> Iter.map(W) |> Iter.to_list())
    io.inspect([a, b] |> Iter.map(fn W(z) -> z) |> Iter.to_list())
    io.inspect(Holder.W(` + c.v1 + `))
    io.inspect(
        case Holder.W(` + c.v2 + `) {
            Holder.W(h) -> h
            Holder.Other -> ` + c.v1 + `
        }
    )
    io.inspect(#S{w: a})
`)
			has := func(d string) bool {
				for _, x := range c.derives {
					if x == d {
						return true
					}
				}
				return false
			}
			if has("Equatable") {
				src.WriteString("    io.print(\"${a == b} ${a == W(" + c.v1 + ")}\")\n")
			}
			if has("Hashable") {
				src.WriteString("    io.inspect(Map.get(%{a => 1, b => 2}, b))\n")
				src.WriteString("    io.print(\"${Set.contains?(%[a], a)} ${Set.contains?(%[a], b)}\")\n")
			}
			if has("Comparable") {
				src.WriteString("    io.print(\"${a < b} ${b < a}\")\n")
			}
			if has("Display") {
				src.WriteString("    io.print(\"${m}\")\n")
			}
			src.WriteString("}\n")
			if got := runDistinctLeaf(t, src.String()); got != c.want {
				t.Fatalf("output\n%s\nwant\n%s\nprogram:\n%s", got, c.want, src.String())
			}
		})
	}
}

// A distinct over a function type destructures in a parameter, a lambda
// parameter, a binding and a `case` arm, and the bound function is called;
// a destructured function parameter declined before ("a destructured name's
// kind outside the domain").
func TestDistinctOverFunction_DestructuresAndCalls(t *testing.T) {
	src := `import std/io

type Rate (Decimal) -> Decimal

fn apply(Rate(f), x: Decimal) -> Decimal {
    f(x)
}

fn main() {
    r = Rate(fn d: Decimal -> d * 2.0d)
    io.inspect(apply(r, 1.25d))
    Rate(g) = r
    io.inspect(g(3.0d))
    io.inspect(
        case r {
            Rate(h) -> h(0.5d)
        }
    )
    io.inspect([r] |> Iter.map(fn Rate(k) -> k(1.0d)) |> Iter.to_list())
    fs: List<(Decimal) -> Decimal> = [fn d: Decimal -> d + 1.0d]
    io.inspect(fs |> Iter.map(Rate) |> Iter.map(fn rr -> apply(rr, 1.0d)) |> Iter.to_list())
}
`
	want := "2.500d\n6.00d\n1.00d\n[2.00d]\n[2.0d]\n"
	if got := runDistinctLeaf(t, src); got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// A list literal of Unit values builds, as a list of any other value does;
// it declined as "a ListLit" before, which left `[Unit, Unit] |>
// Iter.map(Done)` for `type Done Unit` unable to run.
func TestListOfUnit_Builds(t *testing.T) {
	src := `import std/io

fn main() {
    xs = [Unit, Unit]
    io.print("${Iter.count(xs)} ${xs == [Unit, Unit]} ${xs == [Unit]}")
    io.inspect([Unit, ..xs])
    io.inspect(
        case xs {
            [] -> 0
            [_, ..rest] -> Iter.count(rest)
        }
    )
}
`
	want := "2 True False\n[Unit, Unit, Unit]\n1\n"
	if got := runDistinctLeaf(t, src); got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

func runDistinctLeaf(t *testing.T, src string) string {
	t.Helper()
	p, err := vmhost.Load(writeProgram(t, src))
	if err != nil {
		t.Fatalf("the front end rejects the program: %v\n%s", err, src)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, src)
	}
	return out.String()
}
