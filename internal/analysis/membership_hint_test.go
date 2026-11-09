package analysis_test

import (
	"strings"
	"testing"
)

// A missing member of std's Map, Set or List that reads as a membership
// test (`has_key`, `includes`) is the usual no-member error, with a help
// line naming `in`.
func TestCheck_MembershipMemberNamesIn(t *testing.T) {
	const mapKey = "test a key with `k in m` (it calls `Map.contains(m, k)`); `Map.get(m, k)` gives its value as a `Maybe`"
	cases := []struct {
		body, want, hint string
	}{
		{"  m = %{\"a\" => 1}\n  _ = Map.has_key(m, \"a\")\n", "type 'Map' has no member 'has_key'", mapKey},
		{"  m = %{\"a\" => 1}\n  _ = Map.contains_key(m, \"a\")\n", "type 'Map' has no member 'contains_key'", mapKey},
		{"  m = %{\"a\" => 1}\n  _ = Map.has_value(m, 1)\n", "type 'Map' has no member 'has_value'", "test a value with `v in Map.values(m)`; `k in m` tests a key"},
		{"  s = %[\"a\"]\n  _ = Set.has(s, \"a\")\n", "type 'Set' has no member 'has'", "test membership with `x in s` (it calls `Set.contains(s, x)`)"},
		{"  _ = List.includes([1], 1)\n", "type 'List' has no member 'includes'", "test membership with `x in list` (it calls `List.contains(list, x)`)"},
		{"  m = %{\"a\" => 1}\n  _ = m.has_key(\"a\")\n", "Map<String, Int> has no field 'has_key'", mapKey},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			_, errs := checkSourceWithStdlib("fn main() {\n" + c.body + "}\n")
			if len(errs) != 1 {
				t.Fatalf("errors = %v, want exactly %q", errs, c.want)
			}
			e := errs[0]
			if e.Message != c.want {
				t.Errorf("error = %q, want %q", e.Message, c.want)
			}
			if hints := strings.Join(e.Hints, "\n"); !strings.Contains(hints, c.hint) {
				t.Errorf("hints = %q, want them to hold %q", hints, c.hint)
			}
		})
	}
}

// A user type named Map is not std's, and another missing member is not a
// membership test: neither gets the help.
func TestCheck_MembershipHintIsOnlyForStdAndMembershipNames(t *testing.T) {
	for _, src := range []string{
		"struct Map {\n  n: Int\n}\nfn main() {\n  _b = Map.has_key(1)\n}\n",
		"fn main() {\n  m = %{\"a\" => 1}\n  _ = Map.size_of(m)\n}\n",
	} {
		_, errs := checkSourceWithStdlib(src)
		if len(errs) == 0 {
			t.Fatalf("the checker accepts %q", src)
		}
		if got := strings.Join(errs[0].Hints, "\n"); strings.Contains(got, "test membership") || strings.Contains(got, "test a key") {
			t.Errorf("%q: hint = %q, want no membership help", src, got)
		}
	}
}
