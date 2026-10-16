package format

import (
	"strconv"
	"strings"
	"testing"
)

// sevens is the multiples of seven from 7 to 308, written `7, 14, ...`:
// a list of Int literals too wide for any line.
var sevens = func() string {
	var parts []string
	for i := 1; i <= 44; i++ {
		parts = append(parts, strconv.Itoa(i*7))
	}
	return strings.Join(parts, ", ")
}()

// sevensAt is sevens filled at the indent of the given number of spaces,
// as the formatter packs them into lines of 100 columns.
var sevensAt = map[int]string{
	8: "        7, 14, 21, 28, 35, 42, 49, 56, 63, 70, 77, 84, 91, 98, 105, 112, 119, 126, 133, 140, 147,\n" +
		"        154, 161, 168, 175, 182, 189, 196, 203, 210, 217, 224, 231, 238, 245, 252, 259, 266, 273,\n" +
		"        280, 287, 294, 301, 308,\n",
	12: "            7, 14, 21, 28, 35, 42, 49, 56, 63, 70, 77, 84, 91, 98, 105, 112, 119, 126, 133, 140,\n" +
		"            147, 154, 161, 168, 175, 182, 189, 196, 203, 210, 217, 224, 231, 238, 245, 252, 259,\n" +
		"            266, 273, 280, 287, 294, 301, 308,\n",
	16: "                7, 14, 21, 28, 35, 42, 49, 56, 63, 70, 77, 84, 91, 98, 105, 112, 119, 126, 133, 140,\n" +
		"                147, 154, 161, 168, 175, 182, 189, 196, 203, 210, 217, 224, 231, 238, 245, 252, 259,\n" +
		"                266, 273, 280, 287, 294, 301, 308,\n",
}

// A list, vector or set literal of number literals too wide for its line
// breaks after its opening bracket and packs as many elements on each line
// as fit, each followed by a comma, with the closing bracket on a line of
// its own. Where the list stands decides only the indent it packs at.
func TestFormat_NumberListFills(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"binding value",
			"fn main() {\n    xs = [" + sevens + "]\n}\n",
			"fn main() {\n    xs = [\n" + sevensAt[8] + "    ]\n}\n",
		},
		{
			"assertion's right side",
			"test \"t\" {\n    assert g(1) == [" + sevens + "]\n}\n",
			"test \"t\" {\n    assert g(1) == [\n" + sevensAt[8] + "    ]\n}\n",
		},
		{
			"the only call argument",
			"fn main() {\n    f([" + sevens + "])\n}\n",
			"fn main() {\n    f(\n        [\n" + sevensAt[12] + "        ]\n    )\n}\n",
		},
		{
			"a call argument among others",
			"fn main() {\n    f(1, [" + sevens + "], 2)\n}\n",
			"fn main() {\n    f(\n        1,\n        [\n" + sevensAt[12] + "        ],\n        2,\n    )\n}\n",
		},
		{
			"struct literal field",
			"fn main() {\n    p = P{xs: [" + sevens + "], y: 1}\n}\n",
			"fn main() {\n    p = P{\n        xs: [\n" + sevensAt[12] + "        ],\n        y: 1,\n    }\n}\n",
		},
		{
			"pipe head",
			"fn main() {\n    n = [" + sevens + "] |> Iter.count()\n}\n",
			"fn main() {\n    n =\n        [\n" + sevensAt[12] + "        ]\n        |> Iter.count()\n}\n",
		},
		{
			"pipe stage argument",
			"fn main() {\n    n = xs |> Iter.concat([" + sevens + "]) |> Iter.count()\n}\n",
			"fn main() {\n    n =\n        xs\n        |> Iter.concat(\n            [\n" + sevensAt[16] +
				"            ]\n        )\n        |> Iter.count()\n}\n",
		},
		{
			"vector and set",
			"fn main() {\n    v = #[" + sevens + "]\n    s = #{" + sevens + "}\n}\n",
			"fn main() {\n    v = #[\n" + sevensAt[8] + "    ]\n\n    s = #{\n" + sevensAt[8] + "    }\n}\n",
		},
		{
			"typed list",
			"fn main() {\n    xs = Primes[" + sevens + "]\n}\n",
			"fn main() {\n    xs = Primes[\n" + sevensAt[8] + "    ]\n}\n",
		},
		{
			"every number kind",
			"fn main() {\n    xs = [1.5, -2, -3.25, 4d, 1_000_000, 0xFF, 0b1010, 1.0e10, -7d, 12.50d, -0.001, 9_999.999_9, 3, 4, 5, 6]\n}\n",
			"fn main() {\n    xs = [\n" +
				"        1.5, -2, -3.25, 4d, 1_000_000, 0xFF, 0b1010, 1.0e10, -7d, 12.50d, -0.001, 9_999.999_9, 3, 4,\n" +
				"        5, 6,\n" +
				"    ]\n}\n",
		},
		{
			// An element wider than a line takes a line of its own, and the
			// packing goes on after it.
			"element wider than a line",
			"fn main() {\n    xs = [0." + strings.Repeat("1", 118) + ", 1, 2, 3, 0." + strings.Repeat("2", 93) + "]\n}\n",
			"fn main() {\n    xs = [\n" +
				"        0." + strings.Repeat("1", 118) + ",\n" +
				"        1, 2, 3,\n" +
				"        0." + strings.Repeat("2", 93) + ",\n" +
				"    ]\n}\n",
		},
		{
			// The formatter drops parentheses around a literal, so the
			// list fills on the first format, as it does on the next.
			"parenthesized literals",
			"fn main() {\n    xs = [(7), ((14)), -(21), " + strings.TrimPrefix(sevens, "7, 14, 21, ") + "]\n}\n",
			"fn main() {\n    xs = [\n" + strings.Replace(sevensAt[8], " 21,", " -21,", 1) + "    ]\n}\n",
		},
		{
			"fits on one line",
			"fn main() {\n    xs = [\n        1,\n        2,\n        3,\n    ]\n}\n",
			"fn main() {\n    xs = [1, 2, 3]\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatsKeepingMeaning(t, c.src, c.want) })
	}
}

// A line holds an element when the element and its comma end at or before
// the width. Three Floats 29 columns wide take 3*30+2 = 92 columns from
// indent 8, ending at column 100 exactly; a fourth goes on the next line.
// At 30 columns wide only two fit.
func TestFormat_NumberListFillsToTheWidth(t *testing.T) {
	f29 := "0." + strings.Repeat("1", 27)
	line := "        " + f29 + ", " + f29 + ", " + f29 + ","
	if textWidth(line) != defaultWidth {
		t.Fatalf("the line is %d columns wide, not %d", textWidth(line), defaultWidth)
	}
	formatsKeepingMeaning(t,
		"fn main() {\n    xs = ["+strings.Repeat(f29+", ", 3)+f29+"]\n}\n",
		"fn main() {\n    xs = [\n"+line+"\n        "+f29+",\n    ]\n}\n")

	f30 := "0." + strings.Repeat("2", 28)
	formatsKeepingMeaning(t,
		"fn main() {\n    xs = ["+strings.Repeat(f30+", ", 3)+f30+"]\n}\n",
		"fn main() {\n    xs = [\n        "+f30+", "+f30+",\n        "+f30+", "+f30+",\n    ]\n}\n")
}

// The fill does not read the input's layout: one element per line, packed,
// or all on one line, the output is the same.
func TestFormat_NumberListFillIgnoresTheInputLayout(t *testing.T) {
	want := "fn main() {\n    xs = [\n" + sevensAt[8] + "    ]\n}\n"
	for _, src := range []string{
		"fn main() {\n    xs = [" + sevens + "]\n}\n",
		"fn main() {\n    xs = [\n        " + strings.ReplaceAll(sevens, ", ", ",\n        ") + ",\n    ]\n}\n",
		"fn main() {\n    xs = [" + strings.ReplaceAll(sevens, "21, ", "21,\n") + "]\n}\n",
	} {
		formatsKeepingMeaning(t, src, want)
	}
}

// One element per line is kept when any element is not a number literal,
// when the literal has a spread, when any element or the end of the list
// holds a comment, and for every map literal.
func TestFormat_ListFillNeedsNumberLiteralsAndNoComments(t *testing.T) {
	onePerLine := "        " + strings.ReplaceAll(sevens, ", ", ",\n        ") + ",\n"
	for _, c := range []struct{ name, src, want string }{
		{"string", "[" + sevens + ", \"a\"]", "[\n" + onePerLine + "        \"a\",\n    ]"},
		{"codepoint", "[" + sevens + ", 'a']", "[\n" + onePerLine + "        'a',\n    ]"},
		{"boolean", "[" + sevens + ", True]", "[\n" + onePerLine + "        True,\n    ]"},
		{"name", "[" + sevens + ", y]", "[\n" + onePerLine + "        y,\n    ]"},
		{"dot variant", "[" + sevens + ", .Red]", "[\n" + onePerLine + "        .Red,\n    ]"},
		{"call", "[" + sevens + ", f(1)]", "[\n" + onePerLine + "        f(1),\n    ]"},
		{"interpolated string", "[" + sevens + ", \"a${x}\"]", "[\n" + onePerLine + "        \"a${x}\",\n    ]"},
		{"nested list", "[" + sevens + ", [1]]", "[\n" + onePerLine + "        [1],\n    ]"},
		{"tuple", "[" + sevens + ", (1, 2)]", "[\n" + onePerLine + "        (1, 2),\n    ]"},
		{"struct", "[" + sevens + ", P{x: 1}]", "[\n" + onePerLine + "        P{x: 1},\n    ]"},
		{"lambda", "[" + sevens + ", |x| x]", "[\n" + onePerLine + "        |x| x,\n    ]"},
		{"negated name", "[" + sevens + ", -x]", "[\n" + onePerLine + "        -x,\n    ]"},
		{
			"multi-line string",
			"[" + sevens + ", \"\"\"\n        a\n        \"\"\"]",
			"[\n" + onePerLine + "        \"\"\"\n            a\n            \"\"\",\n    ]",
		},
		{"trailing comment", "[" + sevens + ", // c\n    5]", "[\n" + strings.TrimSuffix(onePerLine, ",\n") + ", // c\n        5,\n    ]"},
		{"leading comment", "[\n        // lead\n        " + sevens + "]", "[\n        // lead\n" + onePerLine + "    ]"},
		{"end comment", "[" + sevens + "\n    // end\n    ]", "[\n" + onePerLine + "        // end\n    ]"},
		{"vector spread", "#[..ys, " + sevens + "]", "#[\n        ..ys,\n" + onePerLine + "    ]"},
		{"set spread", "#{..ys, " + sevens + "}", "#{\n        ..ys,\n" + onePerLine + "    }"},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, "fn main() {\n    xs = "+c.src+"\n}\n", "fn main() {\n    xs = "+c.want+"\n}\n")
		})
	}

	var entries, broken []string
	for i := 0; i < 30; i++ {
		entries = append(entries, strconv.Itoa(i)+" => "+strconv.Itoa(i))
		broken = append(broken, "        "+strconv.Itoa(i)+" => "+strconv.Itoa(i)+",\n")
	}
	formatsKeepingMeaning(t,
		"fn main() {\n    m = {"+strings.Join(entries, ", ")+"}\n}\n",
		"fn main() {\n    m = {\n"+strings.Join(broken, "")+"    }\n}\n")
}

// A list of only strings (wide ones too), codepoints or booleans keeps one
// element per line: such a list is usually read an element at a time.
func TestFormat_NonNumberListsStayOnePerLine(t *testing.T) {
	for _, elem := range []string{"\"日本語のテキスト\"", "\"a short string\"", "'x'", "False"} {
		items := strings.Repeat(elem+", ", 29) + elem
		want := "fn main() {\n    xs = [\n" + strings.Repeat("        "+elem+",\n", 30) + "    ]\n}\n"
		formatsKeepingMeaning(t, "fn main() {\n    xs = ["+items+"]\n}\n", want)
	}
}
