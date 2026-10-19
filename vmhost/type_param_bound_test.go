package vmhost_test

import "testing"

// A generic body runs for every type argument its callers pass, so what it
// needs of a type parameter must be one of the parameter's bounds. A use the
// bounds do not cover is an error at the use, with a hint showing the bound
// to add; before, `nomi check` accepted it and a caller passing a type without
// the interface reached a "not supported yet" error instead.
func TestTypeParamBound_UnboundedUseIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		// decl holds `{bound}` where the bound goes.
		decl, bound, want, help string
	}{
		{
			name:  "interface call",
			decl:  "fn f<T>(x: T) -> String{bound} {\n    Display.to_string(x)\n}\n",
			bound: " where T: Display",
			want:  "main.nomi:2:23: `x` is a `T`, and `f` does not require Display of `T`",
			help:  "require it in the signature: `fn f<T>(x: T) -> String where T: Display`",
		},
		{
			name:  "pipe",
			decl:  "fn f<T>(x: T) -> String{bound} {\n    x |> Display.to_string()\n}\n",
			bound: " where T: Display",
			want:  "main.nomi:2:5: `x` is a `T`, and `f` does not require Display of `T`",
			help:  "`fn f<T>(x: T) -> String where T: Display`",
		},
		{
			name:  "interpolation",
			decl:  "fn f<T>(x: T) -> String{bound} {\n    \"<${x}>\"\n}\n",
			bound: " where T: Display",
			want:  "main.nomi:2:9: `x` is a `T`, and `f` does not require Display of `T`",
			help:  "`fn f<T>(x: T) -> String where T: Display`",
		},
		{
			name:  "interpolated list",
			decl:  "fn f<T>(xs: List<T>) -> String{bound} {\n    \"${xs}\"\n}\n",
			bound: " where T: Display",
			want:  "main.nomi:2:8: `xs` holds values of `T`, and `f` does not require Display of `T`",
			help:  "`fn f<T>(xs: List<T>) -> String where T: Display`",
		},
		{
			name:  "ordering",
			decl:  "fn f<T>(a: T, b: T) -> Bool{bound} {\n    a < b\n}\n",
			bound: " where T: Comparable",
			want:  "main.nomi:2:5: `a` is a `T`, and `f` does not require Comparable of `T`",
			help:  "`fn f<T>(a: T, b: T) -> Bool where T: Comparable`",
		},
		{
			name:  "hash",
			decl:  "fn f<T>(x: T) -> Int{bound} {\n    Hashable.hash(x)\n}\n",
			bound: " where T: Hashable",
			want:  "main.nomi:2:19: `x` is a `T`, and `f` does not require Hashable of `T`",
			help:  "`fn f<T>(x: T) -> Int where T: Hashable`",
		},
		{
			name:  "generic call",
			decl:  "fn shown<U>(u: U) -> String where U: Display {\n    Display.to_string(u)\n}\n\nfn f<T>(x: T) -> String{bound} {\n    shown(x)\n}\n",
			bound: " where T: Display",
			want:  "main.nomi:6:11: `x` is a `T`, and `f` does not require Display of `T`",
			help:  "`fn f<T>(x: T) -> String where T: Display`",
		},
		{
			name:  "piped generic call",
			decl:  "fn shown<U>(u: U) -> String where U: Display {\n    Display.to_string(u)\n}\n\nfn f<T>(x: T) -> String{bound} {\n    x |> shown()\n}\n",
			bound: " where T: Display",
			want:  "main.nomi:6:5: `x` is a `T`, and `f` does not require Display of `T`",
			help:  "`fn f<T>(x: T) -> String where T: Display`",
		},
		{
			name:  "sort",
			decl:  "fn f<T>(xs: List<T>) -> List<T>{bound} {\n    Iter.sort(xs)\n}\n",
			bound: " where T: Comparable",
			want:  "main.nomi:2:15: `xs` holds values of `T`, and `f` does not require Comparable of `T`",
			help:  "`fn f<T>(xs: List<T>) -> List<T> where T: Comparable`",
		},
		{
			name:  "second parameter beside a bound one",
			decl:  "fn f<A, B>(a: A, b: B) -> String where A: Display{bound} {\n    \"${a} ${b}\"\n}\n",
			bound: ", B: Display",
			want:  "main.nomi:2:13: `b` is a `B`, and `f` does not require Display of `B`",
			help:  "`fn f<A, B>(a: A, b: B) -> String where A: Display, B: Display`",
		},
		{
			name:  "interface impl block",
			decl:  "struct Box<T> {\n    value: T\n}\n\nimpl Display for Box<T>{bound} {\n    fn to_string(b: Box<T>) -> String {\n        \"[${b.value}]\"\n    }\n}\n",
			bound: " where T: Display",
			want:  "main.nomi:7:13: `b.value` is a `T`, and `impl Display for Box<T>` does not require Display of `T`",
			help:  "require it on the impl block: `impl Display for Box<T> where T: Display`",
		},
		{
			name:  "inherent impl block",
			decl:  "struct Box<T> {\n    value: T\n}\n\nimpl Box<T> {\n    fn show(b: Box<T>) -> String{bound} {\n        \"[${b.value}]\"\n    }\n}\n",
			bound: " where T: Display",
			want:  "main.nomi:7:13: `b.value` is a `T`, and `Box.show` does not require Display of `T`",
			help:  "`fn show(b: Box<T>) -> String where T: Display`",
		},
		{
			name:  "interface type parameter",
			decl:  "interface Holder<T> {\n    fn get(h: self) -> T\n\n    fn shown(h: self) -> String{bound} {\n        \"${get(h)}\"\n    }\n}\n",
			bound: " where T: Display",
			want:  "main.nomi:5:12: this value is a `T`, and `Holder.shown` does not require Display of `T`",
			help:  "`fn shown(h: self) -> String where T: Display`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := func(bound string) string {
				return replaceBound(tc.decl, bound) + "\nfn main() {\n    Unit\n}\n"
			}
			rejects(t, src(""), tc.want, tc.help)
			admits(t, src(tc.bound))
		})
	}
}

func replaceBound(decl, bound string) string {
	const hole = "{bound}"
	for i := 0; i+len(hole) <= len(decl); i++ {
		if decl[i:i+len(hole)] == hole {
			return decl[:i] + bound + decl[i+len(hole):]
		}
	}
	return decl
}

// Debug is universal and `==` compares structurally, so neither needs a bound.
func TestTypeParamBound_DebugAndEqualityNeedNone(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std/io

fn look<T>(x: T) -> String {
    "${Debug.inspect(x)} ${x == x}"
}

type Plain Int

fn main() {
    io.print(look(Plain(3)))
    io.print(look([1, 2]))
}
`})
	if want := "Plain(3) True\n[1, 2] True\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// Each bounded form the error's hint writes runs.
func TestTypeParamBound_BoundedFormsRun(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std/io

fn shown<U>(u: U) -> String where U: Display {
    Display.to_string(u)
}

fn wrap<T>(x: T) -> String where T: Display {
    "<${x}> ${shown(x)} ${x |> shown()}"
}

fn least<T>(xs: List<T>) -> List<T> where T: Comparable {
    Iter.sort(xs)
}

struct Box<T> {
    value: T
}

impl Display for Box<T> where T: Display {
    fn to_string(b: Box<T>) -> String {
        "[${b.value}]"
    }
}

impl Box<T> {
    fn show(b: Box<T>) -> String where T: Display {
        "box ${b.value}"
    }
}

interface Holder<T> {
    fn get(h: self) -> T

    fn told(h: self) -> String where T: Display {
        "holds ${get(h)}"
    }
}

impl Holder<Int> for Box<Int> {
    fn get(b: Box<Int>) -> Int {
        b.value
    }
}

fn main() {
    io.print(wrap(1))
    io.print("${least([3, 1, 2])}")
    io.print("${#Box { value: 4 }}")
    io.print(Box.show(#Box { value: "s" }))
    io.print(Holder.told(#Box { value: 5 }))
    io.print("${[#Box { value: 6 }]}")
}
`})
	if want := "<1> 1 1\n[1, 2, 3]\n[4]\nbox s\nholds 5\n[[6]]\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// `impl Display for List<T> where T: Display`: a list of a type without
// Display is the element's missing impl, named at the call.
func TestTypeParamBound_ConditionalImplNamesTheElement(t *testing.T) {
	rejects(t, `import std/io

type Foo Int

fn show<T>(value: T) -> String where T: Display {
    Display.to_string(value)
}

fn main() {
    io.print(show([Foo(1)]))
}
`, "no impl of `Display` for `Foo`")
}
