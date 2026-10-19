package vmhost_test

import "testing"

// A field name may start with `_` in a struct-shaped variant as in a struct.
// The bodies synthesized for an enum (universal Debug and every derive)
// bind each field of a struct variant under a name of their own; that name
// once ended in the field name's suffix (`_w_v`), which a leading `_` made a
// discard, and the enum failed with "undefined variable '_w_v'". derive
// FromJson on a struct bound `_a_value` the same way. A program that builds,
// matches, compares, hashes, encodes and prints such values runs.
func TestUnderscoreField_VariantAndStructFieldsRun(t *testing.T) {
	got := runSourceOutput(t, `import std/io
import std/json.{ToJson, FromJson}

enum Plain {
    Trim {_w: Int}
    Other
}

enum E {
    Trim {_w: Int, __x: String, y: Int}
    Pair #(Int, String)
    Other
}

derive Equatable for E
derive Hashable for E
derive Comparable for E
derive Display for E
derive Debug for E
derive ToJson for E

struct S {
    _a: Int
    b: Int
}

derive Equatable for S
derive Hashable for S
derive Comparable for S
derive Display for S
derive Debug for S
derive ToJson for S
derive FromJson for S

type W #(Int, String)

derive Equatable for W
derive Debug for W

enum G<T> {
    Hold {_v: T}
    Empty
}

derive Equatable for G<T>
derive Debug for G<T>
derive Display for G<T> where T: Display

fn main() {
    io.inspect(#Plain.Trim {_w: 1})
    e = #E.Trim {_w: 3, __x: "x", y: 4}
    case e {
        #E.Trim {_w: n, __x: s, y} -> io.print("${n} ${s} ${y}")
        E.Pair(#(a, b)) -> io.print("${a}${b}")
        E.Other -> io.print("0")
    }
    io.inspect(e)
    io.print(e)
    io.print(e == #E.Trim {_w: 3, __x: "x", y: 4})
    io.print(Comparable.compare(e, #E.Trim {_w: 2, __x: "x", y: 4}))
    io.print(Hashable.hash(e) == Hashable.hash(#E.Trim {_w: 3, __x: "x", y: 4}))
    io.print(ToJson.to_json(e))
    io.print(ToJson.to_json(E.Pair(#(1, "p"))))
    s = #S{_a: 1, b: 7}
    io.print(s)
    io.print(ToJson.to_json(s))
    io.print(S.from_json(ToJson.to_json(#S{_a: 2, b: 3})))
    io.print(W(#(1, "w")) == W(#(1, "w")))
    io.inspect(W(#(1, "w")))
    g = #G.Hold {_v: 5}
    io.inspect(g)
    io.print(g)
    io.print(g == #G.Hold {_v: 5})
    case g {
        #G.Hold {_v: v} -> io.print(v)
        G.Empty -> io.print("empty")
    }
}
`)
	want := `#Trim{_w: 1}
3 x 4
#Trim{_w: 3, __x: "x", y: 4}
#Trim{_w: 3, __x: x, y: 4}
True
Greater
True
{"Trim":{"_w":3,"__x":"x","y":4}}
{"Pair":[1,"p"]}
#S{_a: 1, b: 7}
{"_a":1,"b":7}
Ok(#S{_a: 2, b: 3})
True
#W(1, "w")
#Hold{_v: 5}
#Hold{_v: 5}
True
5
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
