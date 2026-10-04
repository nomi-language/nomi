package irbuild

import "testing"

// TestDebugIter_ViewedSourcesRender pins Debug of a declared `Iter<T>`
// position. A view inspects as its source (spec §16), so the rendering must be
// told the Debug impl of every source that position can hold: std's generic
// Range impl, a user `Iter` implementor's, and a declared element's inside a
// List, Set or Map. Each of rows 1 to 10 and the dbg line was BLOCKED with
// "Debug on an unrepresented receiver" before the renderer named them.
// Rows 11 and 12 are the controls that never needed an impl, and row 13 shows
// inspecting consumed nothing.
func TestDebugIter_ViewedSourcesRender(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "1 range    1..=3\n" +
		"2 user     Count{n: 2}\n" +
		"3 list     [Point{x: 1, y: 2}]\n" +
		"4 set      #{Point{x: 3, y: 4}}\n" +
		"5 map      {\"p\" => Point{x: 5, y: 6}}\n" +
		"6 generic  Count{n: 1}\n" +
		"7 generic  [Point{x: 0, y: 0}]\n" +
		"8 field    Holder{items: 1..=4}\n" +
		"9 field    Holder{items: Count{n: 3}}\n" +
		"10 payload Some(2..=3)\n" +
		"2..=3\n" +
		"dbg line 49: xs = [Point{x: 7, y: 8}]\n" +
		"11 ints    [4, 5]\n" +
		"12 lazy    <iter>\n" +
		"13 after   [2, 3]\n"
	got := vmReference(fixture("debug_iter_sources.nomi"))
	if got.stdout != want || got.exit != 0 {
		t.Errorf("VM transcript drifted (exit %d, stderr %q):\n--- got ---\n%s\n--- want ---\n%s",
			got.exit, got.stderr, got.stdout, want)
	}
}
