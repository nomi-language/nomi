package format

import "testing"

// A bare `..` (`{.., city: "NYC"}`, `[t, ..]`, `#{.., x}`, `#[.., x]`) lays
// out as the spread it is, with nothing after the `..`: flat when it fits,
// one element per line when it does not or when it holds a comment.

func TestFormat_CurrentValueInline(t *testing.T) {
	roundTrip(t, `fn main() {
    a = {..user, address: {.., city: "NYC"}}
    b = %{..team, "home" => %{.., "city" => "Atlanta"}}
    c = {..account, log: [t, ..], seen: #{.., 3}}
    d = {..account, recent: #[.., 9], first: #[0, ..]}
    e = Struct.update(user, {address: {.., zip: "1"}})
    f = #{..s, 3, 4}
}
`)
}

func TestFormat_CurrentValueSpacing(t *testing.T) {
	migrates(t, `fn main() {
    a = {..user, address: { .. , city : "NYC" }}
    b = {..account, log: [t,..], seen: #{ ..,3}, recent: #[ .. ,9]}
}
`, `fn main() {
    a = {..user, address: {.., city: "NYC"}}
    b = {..account, log: [t, ..], seen: #{.., 3}, recent: #[.., 9]}
}
`)
}

func TestFormat_CurrentValueBreaksByWidth(t *testing.T) {
	migrates(t, `fn main() {
    a = {..user, address: {.., street: "a fairly long street name here", city: "a fairly long city name too, longer"}}
}
`, `fn main() {
    a = {
        ..user,
        address: {
            ..,
            street: "a fairly long street name here",
            city: "a fairly long city name too, longer",
        },
    }
}
`)
}

func TestFormat_CurrentValueKeepsComments(t *testing.T) {
	roundTrip(t, `fn main() {
    a = {
        ..user,
        address: {
            .., // the current address
            // only the city moves
            city: "NYC",
        },
    }

    b = #{
        // the old set first
        ..,
        3,
    }
}
`)
}
