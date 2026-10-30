package format

import "testing"

// A postfix `!` stays against the expression it unwraps, in every position.
func TestUnwrap_KeepsTheBang(t *testing.T) {
	roundTrip(t, `fn main() {
    a = half(4)!
    b = Map.get(m, "k")!
    c = Row.get<Int>(row, "n")!
    d = "4" |> String.to_int()! |> Int.abs()
    e = user.address!.city
    f = Iter.map(xs, &half(&1)!)
    g = !found! and x! == y
    h = (a |> b())!
}
`)
	migrates(t, "fn main() {\n    a = Map.get( m,\"k\" )!\n}\n", "fn main() {\n    a = Map.get(m, \"k\")!\n}\n")
}
