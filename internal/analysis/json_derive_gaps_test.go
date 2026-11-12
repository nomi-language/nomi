package analysis_test

import "testing"

// A tuple implements neither ToJson nor FromJson, and std implements both
// for Map<String, V> alone: a derive over a field holding either is
// refused at the field, through aliases and std's containers, and so is
// an interface-qualified call on one.
func TestJsonRejectsTuplesAndMapsWithOtherKeys(t *testing.T) {
	derives := []struct {
		name, decls, want string
	}{
		{"a tuple field", "struct W\n  v: (Int, String)\nend\n\nderive ToJson for W\nderive FromJson for W\n",
			"no impl of `ToJson` for `(Int, String)` (required via `derive ToJson for W`, field `v`)\n" +
				"help: a tuple implements neither `ToJson` nor `FromJson`"},
		{"a tuple field under FromJson", "struct W\n  v: (Int, String)\nend\n\nderive FromJson for W\n",
			"no impl of `FromJson` for `(Int, String)` (required via `derive FromJson for W`, field `v`)"},
		{"a Map with an Int key", "struct W\n  m: Map<Int, Int>\nend\n\nderive ToJson for W\nderive FromJson for W\n",
			"no impl of `ToJson` for `Map<Int, Int>` (required via `derive ToJson for W`, field `m`)\n" +
				"help: std implements `ToJson` and `FromJson` for `Map<String, V>` alone"},
		{"a Map with an interface key", "struct W\n  m: Map<Hashable, Int>\nend\n\nderive FromJson for W\n",
			"no impl of `FromJson` for `Map<Hashable, Int>` (required via `derive FromJson for W`, field `m`)"},
		{"an aliased tuple key in a list", "typealias Pt (Int, Int)\n\nstruct W\n  a: List<Map<Pt, Int>>\nend\n\nderive ToJson for W\n",
			"no impl of `ToJson` for `Map<Pt, Int>` (required via `derive ToJson for W`, field `a`)"},
	}
	for _, tc := range derives {
		t.Run(tc.name, func(t *testing.T) {
			fa := buildWithStdlibForDerive(anonJsonDecls + "\n" + tc.decls)
			if !hasErrSubstring(fa.TypeErrors, tc.want) {
				t.Fatalf("the derive admits the field, or rejects it with another message; want %q, got:\n%s",
					tc.want, errorsText(fa.TypeErrors))
			}
		})
	}
	calls := []struct {
		stmt, want string
	}{
		{`_ = ToJson.to_json((1, 2))`, "(Int, Int) does not implement ToJson (required by `ToJson.to_json`)\n" +
			"help: no impl can be written for a tuple"},
		{`_ = ToJson.to_json_field({1 => 2})`, "Map<Int, Int> does not implement ToJson (required by `ToJson.to_json_field`)"},
		{`_ = FromJson.from_json<Map<Int, Int>>(Json.Null)`, "Map<Int, Int> does not implement FromJson (required by `FromJson.from_json`)"},
	}
	for _, tc := range calls {
		t.Run(tc.stmt, func(t *testing.T) {
			fa := buildWithStdlibForDerive(anonJsonDecls + "\nfn run() -> Unit\n  " + tc.stmt + "\nend\n")
			if !hasErrSubstring(fa.TypeErrors, tc.want) {
				t.Fatalf("the checker admits %s, or rejects it with another message; want %q, got:\n%s",
					tc.stmt, tc.want, errorsText(fa.TypeErrors))
			}
		})
	}
	// A Map keyed by String holding anonymous structs is JSON.
	fa := buildWithStdlibForDerive(anonJsonDecls + "\nstruct W\n  m: Map<String, List<{x: Int}>>\nend\n\n" +
		"derive ToJson for W\nderive FromJson for W\n\nfn run() -> Unit\n  _ = ToJson.to_json({\"a\" => 1})\nend\n")
	if len(fa.TypeErrors) != 0 {
		t.Fatalf("the checker rejects a Map keyed by String:\n%s", errorsText(fa.TypeErrors))
	}
	// So is one keyed by an alias of String: the derive's component type
	// is the alias's target.
	fa = buildWithStdlibForDerive(anonJsonDecls + "\ntypealias Key String\n\n" +
		"struct W\n  m: Map<Key, Int>\n  n: Maybe<Map<Key, List<Key>>>\nend\n\nderive ToJson for W\nderive FromJson for W\n")
	if len(fa.TypeErrors) != 0 {
		t.Fatalf("the checker rejects a Map keyed by an alias of String:\n%s", errorsText(fa.TypeErrors))
	}
}
