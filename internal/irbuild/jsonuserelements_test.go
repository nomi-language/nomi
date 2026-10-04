package irbuild

import "testing"

// std's generic JSON impls at a user element type. `impl FromJson for List<T>`
// calls `T.from_json(item)`, a bounded call with no receiver operand, so the
// instance finds the user's impl by T's declaration (kindImplCall); a nested
// container's `ToJson.to_json(item)` is std's own container impl, instantiated
// from inside the instance (ifaceContainerCall). Iter.sort and Iter.to_set
// over the same type are pinned beside them.
func TestJSONUserElements_StdInstancesCallTheUsersImpl(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "" +
		"dbg line 31: notes = [Note{title: \"b\"}, Note{title: \"a\"}]\n" +
		"dbg line 33: turbo = [Note{title: \"c\"}]\n" +
		"dbg line 35: by_key = {\"k\" => Note{title: \"d\"}}\n" +
		"dbg line 37: maybe = Some(Note{title: \"e\"})\n" +
		"dbg line 39: none = None\n" +
		"dbg line 41: nested = [[Note{title: \"f\"}], []]\n" +
		// The element's shape error carries the list index before the
		// field: std's decode_list prepends it to the user's impl's error.
		"dbg line 43: bad = Err(Json.ShapeError{path: [\"[0]\", \"title\"], expected: \"string\", got: \"int\"})\n" +
		"dbg line 44: Json.encode(ToJson.to_json(notes)) = \"[{\\\"title\\\":\\\"b\\\"},{\\\"title\\\":\\\"a\\\"}]\"\n" +
		"dbg line 45: Json.encode(ToJson.to_json(nested)) = \"[[{\\\"title\\\":\\\"f\\\"}],[]]\"\n" +
		"dbg line 46: Json.encode(ToJson.to_json([by_key])) = \"[{\\\"k\\\":{\\\"title\\\":\\\"d\\\"}}]\"\n" +
		"dbg line 47: Json.encode(ToJson.to_json([maybe, none])) = \"[{\\\"title\\\":\\\"e\\\"},null]\"\n" +
		"dbg line 48: Iter.sort(notes) = [Note{title: \"a\"}, Note{title: \"b\"}]\n" +
		"dbg line 49: Iter.to_set(notes) |> Set.contains?(Note{title: \"a\"}) = True\n"
	got := vmReference(fixture("json_user_elements.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("std's JSON instances at a user element type:\nwant stdout=%q\ngot  %s", want, got)
	}
}

// The same bounded call where the type is another file's, which the calling
// file names only as `notes.Note`: in a user generic (`round`) and in std's
// `List<T>` instance.
func TestJSONUserElements_AnotherFilesType(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "" +
		"{\"title\":\"x\"}\n" +
		"[{\"title\":\"x\"},{\"title\":\"x\"}]\n" +
		"{\"title\":\"y\"}\n" +
		"expected array, got object\n"
	got := vmReference(fixture("json_crossfile/main.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("a bounded call at another file's type:\nwant stdout=%q\ngot  %s", want, got)
	}
}
