package vmhost_test

import (
	"bytes"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// Map update by spread, `%{..base, key => value}`, runs as
// `Map.merge(base, %{key => value})`: the base's entries in their order, a
// literal key replacing the base's value in place, a new key appended in
// the order written.
func TestMapSpread_Runs(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Settings {
    name: String
    prefs: Map<String, String>
}

fn with_entry<K, V>(m: Map<K, V>, k: K, v: V) -> Map<K, V> {
    %{..m, k => v}
}

fn main() {
    base = %{"a" => 1, "b" => 2}
    io.print(%{..base, "c" => 3})
    io.print(%{..base, "a" => 10})
    io.print(%{..base, "z" => 26, "b" => 20, "c" => 3})
    io.print(base)
    s = #Settings{name: "x", prefs: %{"theme" => "light", "font" => "mono"}}
    dark = #{..s, prefs: %{..s.prefs, "theme" => "dark"}}
    io.print(dark.prefs)
    io.print(s.prefs)
    nested = %{"outer" => %{"k" => 1}, "other" => %{"q" => 0}}
    inner = %{"k" => 1}
    io.print(%{..nested, "outer" => %{..inner, "j" => 2}})
    io.print(%{..nested, "outer" => %{"j" => 2}})
    io.print(with_entry(%{1 => "one"}, 2, "two"))
    io.print(with_entry(%{"x" => 1.5}, "x", 2.5))
    scores: Map<String, Maybe<Int>> = %{"ada" => Some(3)}
    io.print(%{..scores, "bob" => None})
    lists = %{"a" => [1]}
    io.print(%{..lists, "b" => []})
    io.print(%{..%{}, "a" => 1})
    piped = base |> then fn m -> %{..m, "p" => 9}
    io.print(piped)
}
`)
	want := `%{a => 1, b => 2, c => 3}
%{a => 10, b => 2}
%{a => 1, b => 20, z => 26, c => 3}
%{a => 1, b => 2}
%{theme => dark, font => mono}
%{theme => light, font => mono}
%{outer => %{k => 1, j => 2}, other => %{q => 0}}
%{outer => %{j => 2}, other => %{q => 0}}
%{1 => one, 2 => two}
%{x => 2.5}
%{ada => Some(3), bob => None}
%{a => [1], b => []}
%{a => 1}
%{a => 1, b => 2, p => 9}
`
	if got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
}

// In an assertion, both a passing and a failing one.
func TestMapSpread_InAnAssertion(t *testing.T) {
	p, err := vmhost.LoadSource("main_test.nomi", `test "spread equals merge" {
    m = %{"a" => 1, "b" => 2}
    assert %{..m, "b" => 5} == Map.merge(m, %{"b" => 5})
    assert %{..m, "c" => 3} == m + %{"c" => 3}
}

test "spread replaces" {
    m = %{"a" => 1}
    assert %{..m, "a" => 2} == m
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	var out bytes.Buffer
	cases := p.Cases(&out, vmhost.TestOptions{})
	if len(cases) != 2 {
		t.Fatalf("cases = %d, want 2", len(cases))
	}
	if c := cases[0]; c.Blocked != nil || c.Err != nil {
		t.Errorf("%s: blocked %v, err %v; want a pass", c.Name, c.Blocked, c.Err)
	}
	if c := cases[1]; c.Blocked != nil || c.Err == nil {
		t.Errorf("%s: blocked %v, err %v; want a failed assertion", c.Name, c.Blocked, c.Err)
	}
}
