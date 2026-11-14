package format

import "testing"

// A bare `..` (`{.., city: "NYC"}`, `[t, ..]`, `[.., x]`, `[.., x]`) lays
// out as the spread it is, with nothing after the `..`: flat when it fits,
// one element per line when it does not or when it holds a comment.

func TestFormat_CurrentValueInline(t *testing.T) {
	roundTrip(t, `fn main()
    a = {..user, address: {.., city: "NYC"}}
    b = {..team, "home" => {.., "city" => "Atlanta"}}
    c = {..account, log: [t, ..], seen: Set[.., 3]}
    d = {..account, recent: Vector[.., 9], first: Vector[0, ..]}
    e = Struct.update(user, {address: {.., zip: "1"}})
    f = Set[..s, 3, 4]
end
`)
}

// A `..` operand writes its `..` as an operand: spaced around a binary
// operator and a pipe, bare in an argument list.
func TestFormat_CurrentValueOperand(t *testing.T) {
	roundTrip(t, `fn main()
    a = {..user, visits: .. + 1, name: String.trim(..), tags: .. |> List.reverse()}
    b = {..user, address: {.., street: Maybe.map(m, fn s -> s + ..)}}
    c = {..counts, "a" => .. - 1}
end
`)
	migrates(t, `fn main()
    a = {..user, visits: ..+1, name: String.trim( .. ), n: ..-1}
end
`, `fn main()
    a = {..user, visits: .. + 1, name: String.trim(..), n: .. - 1}
end
`)
}

func TestFormat_CurrentValueSpacing(t *testing.T) {
	migrates(t, `fn main()
    a = {..user, address: { .. , city : "NYC" }}
    b = {..account, log: [t,..], seen: Set[ ..,3], recent: Vector[ .. ,9]}
end
`, `fn main()
    a = {..user, address: {.., city: "NYC"}}
    b = {..account, log: [t, ..], seen: Set[.., 3], recent: Vector[.., 9]}
end
`)
}

func TestFormat_CurrentValueBreaksByWidth(t *testing.T) {
	migrates(t, `fn main()
    a = {..user, address: {.., street: "a fairly long street name here", city: "a fairly long city name too, longer"}}
end
`, `fn main()
    a = {
        ..user,
        address: {
            ..,
            street: "a fairly long street name here",
            city:   "a fairly long city name too, longer",
        },
    }
end
`)
}

func TestFormat_CurrentValueKeepsComments(t *testing.T) {
	roundTrip(t, `fn main()
    a = {
        ..user,
        address: {
            .., // the current address
            // only the city moves
            city: "NYC",
        },
    }

    b = Set[
        // the old set first
        ..,
        3,
    ]
end
`)
}
