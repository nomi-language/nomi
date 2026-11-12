package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const accessorHoverDecls = `type Pair (Int, String)

struct Address
  city: String
end

struct User
  /// The name the user signs in with.
  name: String
  address: Address
end

`

// A field accessor hovers as the function it is, with the types the checker
// solved where it stands, over every character of it, its dots included,
// and a struct field's doc after it.
func TestHover_FieldAccessor(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"a tuple item", `fn main()
  rules = [(1, "a")]
  _ = rules |> Iter.map(▮.second) |> Iter.to_list()
end
`, "```nomi\n.second: ((Int, String)) -> String\n```"},
		{"a named tuple's item", `fn main()
  _ = [Pair(1, "a")] |> Iter.map(▮.first) |> Iter.to_list()
end
`, "```nomi\n.first: (Pair) -> Int\n```"},
		{"a struct field with a doc", `fn names(users: List<User>) -> List<String>
  Iter.map(users, ▮.name) |> Iter.to_list()
end
`, "```nomi\n.name: (User) -> String\n```\n\nThe name the user signs in with."},
		{"a path, in a pipe", `fn cities(users: List<User>) -> List<String>
  users |> Iter.map(▮.address.city) |> Iter.to_list()
end
`, "```nomi\n.address.city: (User) -> String\n```"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, pos := hoverMarkerPosition(t, accessorHoverDecls+tc.body)
			line := []rune(stringsLine(src, pos.Line))
			end := pos.Col - 1
			for end < len(line) && (line[end] == '.' || line[end] == '_' || line[end] >= 'a' && line[end] <= 'z') {
				end++
			}
			for col := pos.Col - 1; col < end; col++ {
				got, rng := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(col)})
				if got != tc.want {
					t.Fatalf("hover on column %d = %q, want %q", col+1, got, tc.want)
				}
				if rng == nil || rng.Start.Character != uint32(pos.Col-1) || rng.End.Character != uint32(end) {
					t.Fatalf("hover range = %+v, want the whole accessor", rng)
				}
			}
		})
	}
}

func stringsLine(src string, n int) string {
	line := 1
	start := 0
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			if line == n {
				return src[start:i]
			}
			line++
			start = i + 1
		}
	}
	return src[start:]
}
