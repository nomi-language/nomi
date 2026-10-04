package format

import "testing"

func TestFormat_FieldAccessorRoundTrips(t *testing.T) {
	roundTrip(t, `fn f(users: List<User>): List<String> {
    users |> Iter.map(.name) |> Iter.to_list()
}

fn g(users: List<User>): List<String> {
    Iter.map(users, .address.city) |> Iter.to_list()
}

fn h(pairs: List<(Int, String)>): List<String> {
    pairs |> Iter.map(.1) |> Iter.to_list()
}

fn name_of(): (User) -> String {
    .name
}
`)
}
