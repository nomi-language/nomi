package format

import (
	"strings"
	"testing"
)

// Each alignment rule, on its own: the source written with single spaces
// formats to the aligned form, which is a fixed point.
func TestFormat_AlignsEachRule(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"case arms at ->",
			"fn f(n: Int) -> String\n    case n\n        0 -> \"zero\"\n        100 -> \"hundred\"\n        _ -> \"many\"\n    end\nend\n",
			"fn f(n: Int) -> String\n    case n\n        0   -> \"zero\"\n        100 -> \"hundred\"\n        _   -> \"many\"\n    end\nend\n",
		},
		{
			"case without a subject",
			"fn f(n: Int) -> String\n    case\n        n > 0 -> \"pos\"\n        _ -> \"other\"\n    end\nend\n",
			"fn f(n: Int) -> String\n    case\n        n > 0 -> \"pos\"\n        _     -> \"other\"\n    end\nend\n",
		},
		{
			"struct fields, default no column",
			"struct Monster\n    name: String\n    at: Place\n    hit_points: Int = 10\nend\n",
			"struct Monster\n    name:       String\n    at:         Place\n    hit_points: Int = 10\nend\n",
		},
		{
			"struct literal values",
			"fn f() -> Monster\n    Monster{\n        name: \"goblin\",\n        hit_points: 10,\n    }\nend\n",
			"fn f() -> Monster\n    Monster{\n        name:       \"goblin\",\n        hit_points: 10,\n    }\nend\n",
		},
		{
			"named arguments",
			"fn f()\n    spawn(\n        name: \"goblin\",\n        hit_points: 10,\n        at: here_and_there_and_everywhere_and_back_again_and_then_some_more_places,\n    )\nend\n",
			"fn f()\n    spawn(\n        name:       \"goblin\",\n        hit_points: 10,\n        at:         here_and_there_and_everywhere_and_back_again_and_then_some_more_places,\n    )\nend\n",
		},
		{
			"parameters",
			"fn spawn(\n    name: String,\n    hit_points: Int,\n    at: PlaceWhereItAllHappensAndThenSomeMoreOfIt,\n    extra: Int = 0,\n) -> Monster\n    todo\nend\n",
			"fn spawn(\n    name:       String,\n    hit_points: Int,\n    at:         PlaceWhereItAllHappensAndThenSomeMoreOfIt,\n    extra:      Int = 0,\n) -> Monster\n    todo\nend\n",
		},
		{
			"map entries at =>",
			"fn f() -> Map<String, Int>\n    {\n        \"a\" => 1,\n        \"bbbbbb\" => 2,\n    }\nend\n",
			"fn f() -> Map<String, Int>\n    {\n        \"a\"      => 1,\n        \"bbbbbb\" => 2,\n    }\nend\n",
		},
		{
			"rows of tuples",
			"fn f() -> List<(String, Int, Bool)>\n    [\n        (\"Mei\", 100, True),\n        (\"Alexander\", 7, False),\n        (\"Jo\", 12345, True),\n    ]\nend\n",
			"fn f() -> List<(String, Int, Bool)>\n    [\n        (\"Mei\",       100,   True),\n        (\"Alexander\", 7,     False),\n        (\"Jo\",        12345, True),\n    ]\nend\n",
		},
		{
			"rows of calls with one head",
			"fn f() -> List<Point>\n    [\n        point(1, 200),\n        point(33333, 4),\n    ]\nend\n",
			"fn f() -> List<Point>\n    [\n        point(1,     200),\n        point(33333, 4),\n    ]\nend\n",
		},
		{
			"trailing comments",
			"fn f()\n    x = 1 // one\n    longer_name = 2 // two\nend\n",
			"fn f()\n    x = 1           // one\n    longer_name = 2 // two\nend\n",
		},
		{
			"once declarations at =",
			"once small = 1\nonce much_larger: Int = 2\n",
			"once small            = 1\nonce much_larger: Int = 2\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatsKeepingMeaning(t, c.src, c.want) })
	}
}

// A blank line, a comment line or an item over several lines ends a block;
// each run on either side aligns on its own.
func TestFormat_AlignmentBlocksEndAtBlankCommentAndMultiLineItems(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"blank line",
			"struct S\n    a: Int\n    bbb: Int\n\n    cccccc: Int\n    d: Int\nend\n",
			"struct S\n    a:   Int\n    bbb: Int\n\n    cccccc: Int\n    d:      Int\nend\n",
		},
		{
			"comment line",
			"struct S\n    a: Int\n    bbb: Int\n    // later\n    cccccc: Int\n    d: Int\nend\n",
			"struct S\n    a:   Int\n    bbb: Int\n    // later\n    cccccc: Int\n    d:      Int\nend\n",
		},
		{
			"multi-line item",
			"fn f() -> Monster\n    Monster{\n        a: 1,\n        bbb: 2,\n        place: Place{\n            x: 1,\n        },\n        cccccc: 3,\n        d: 4,\n    }\nend\n",
			"fn f() -> Monster\n    Monster{\n        a:   1,\n        bbb: 2,\n        place: Place{\n            x: 1,\n        },\n        cccccc: 3,\n        d:      4,\n    }\nend\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatsKeepingMeaning(t, c.src, c.want) })
	}
}

// A case whose arms put their bodies below `->` aligns nothing.
func TestFormat_BrokenArmsDoNotAlign(t *testing.T) {
	src := "fn f(n: Int) -> String\n    case n\n        0 ->\n            \"zero\"\n\n        100 ->\n            a_long_name_for_a_value_that_goes_on_and_on\n            |> with_a_stage_that_needs_room\n            |> and_another_one\n    end\nend\n"
	roundTrip(t, src)
}

// Left parts more than alignCutoff columns apart stay as they are, for every
// rule but rows.
func TestFormat_AlignmentCutoff(t *testing.T) {
	far := strings.Repeat("x", alignCutoff+2)
	roundTrip(t, "struct S\n    a: Int\n    "+far+": Int\nend\n")
	roundTrip(t, "fn f()\n    x = 1 // one\n    "+far+" = 2 // two\nend\n")
	near := strings.Repeat("x", alignCutoff)
	formatsKeepingMeaning(t,
		"struct S\n    a: Int\n    "+near+"b: Int\nend\n",
		"struct S\n    a:"+strings.Repeat(" ", alignCutoff+1)+"Int\n    "+near+"b: Int\nend\n")
	// Rows have none.
	formatsKeepingMeaning(t,
		"fn f() -> List<(String, Int)>\n    [\n        (\"a\", 1),\n        (\""+far+"\", 2),\n    ]\nend\n",
		"fn f() -> List<(String, Int)>\n    [\n        (\"a\","+strings.Repeat(" ", len(far))+"1),\n        (\""+far+"\", 2),\n    ]\nend\n")
}

// Alignment never pushes a line past the width: a block that would leave
// as it is, and the line breaks are the ones the unaligned text has.
func TestFormat_AlignmentKeepsTheWidth(t *testing.T) {
	long := strings.Repeat("v", 80)
	roundTrip(t, "fn f() -> S\n    S{\n        a: \""+long+"\",\n        bbbbbbbbbbbb: 1,\n    }\nend\n")
}

// Not aligned: local bindings' `=`, enum payloads, one-line literals.
func TestFormat_WhatDoesNotAlign(t *testing.T) {
	roundTrip(t, "fn f()\n    x = 1\n    longer = 2\nend\n")
	roundTrip(t, "enum Shape\n    Dot Int\n    Rectangle {w: Int, h: Int}\nend\n")
	roundTrip(t, "fn f() -> List<(String, Int)>\n    [(\"a\", 1), (\"bbb\", 22)]\nend\n")
	// Rows of different lengths or heads are not a table.
	roundTrip(t, "fn f() -> List<P>\n    [\n        p(1, 200),\n        q(33333, 4),\n    ]\nend\n")
}

// A comment may hold bytes that are not UTF-8, the mark bytes among them:
// they come back as written and are never read as marks.
func TestFormat_CommentBytesAreNeverMarks(t *testing.T) {
	for _, src := range []string{
		"// \xff\xfe\xffa1.2.0\xfe\nx = 1\n",
		"struct S\n    a:   Int // \xffc\xfe\n    bbb: Int // \xfe\nend\n",
	} {
		roundTrip(t, src)
	}
}

// An item aligns at its own marks, not those of a one-line literal inside
// it: here a key that is itself a map.
func TestFormat_AlignmentUsesTheItemsOwnColumn(t *testing.T) {
	formatsKeepingMeaning(t,
		"fn f() -> Map<Map<String, Int>, Int>\n    {\n        {\"a\" => 1} => 1,\n        {} => 2,\n    }\nend\n",
		"fn f() -> Map<Map<String, Int>, Int>\n    {\n        {\"a\" => 1} => 1,\n        {}         => 2,\n    }\nend\n")
}

// Wide characters count two columns, as the layout measures them.
func TestFormat_AlignmentMeasuresDisplayWidth(t *testing.T) {
	formatsKeepingMeaning(t,
		"fn f() -> Map<String, Int>\n    {\n        \"日本\" => 1,\n        \"abcdef\" => 2,\n    }\nend\n",
		"fn f() -> Map<String, Int>\n    {\n        \"日本\"   => 1,\n        \"abcdef\" => 2,\n    }\nend\n")
}
