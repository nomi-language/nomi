package irbuild

import "testing"

// `dbg` over another file's types. Each value's Debug impl lives in the
// declaring file, so the rendering is that file's impl: nestedDebugImpls names
// it for a value alone or inside a container, and anything else renders as
// `Debug.inspect` of the operand does (debugOver). `dbg` was BLOCKED as
// `*ast.Dbg` for every row while `Debug.inspect` of the bare value ran, and
// `Debug.inspect((n, w))` was BLOCKED too.
func TestDbgCrossFile_RendersAnotherFilesTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "" +
		"dbg line 15: n = Note{title: \"x\"}\n" +
		"dbg line 16: m = Loud(3)\n" +
		"dbg line 17: d = Meters(4)\n" +
		"dbg line 18: s = <opaque Secret>\n" +
		"dbg line 19: w = Shown<5>\n" +
		"dbg line 20: b = Box{v: 1}\n" +
		"dbg line 21: [n] = [Note{title: \"x\"}]\n" +
		"dbg line 22: [d] = [Meters(4)]\n" +
		"dbg line 23: (n, w) = (Note{title: \"x\"}, Shown<5>)\n" +
		"dbg line 24: (b, n) = (Box{v: 1}, Note{title: \"x\"})\n" +
		"dbg line 25: Some(m) = Some(Loud(3))\n" +
		"dbg line 26: {\"k\" => w} = {\"k\" => Shown<5>}\n" +
		"dbg line 27: n = Note{title: \"x\"}\n" +
		"dbg line 29: n = Note{title: \"x\"}\n" +
		"Note{title: \"x\"}\n" +
		"(Note{title: \"x\"}, Shown<5>)\n"
	got := vmReference(fixture("dbg_crossfile/main.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("dbg over another file's types:\nwant stdout=%q\ngot  %s", want, got)
	}
}
