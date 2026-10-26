package rt

import "testing"

// A String's literal, which is its Debug, its assertion row and what
// `String.to_literal` answers.
func TestStringLiteral_EscapesWhatWouldNotReadBack(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\nb", `"a\nb"`},
		{"a\tb", `"a\tb"`},
		{"a\rb", `"a\rb"`},
		{`a\b`, `"a\\b"`},
		{`say "hi"`, `"say \"hi\""`},
		{"${x}", `"\${x}"`},
		{"$5 and {", `"$5 and {"`},
		{"$", `"$"`},
		{"\x00", `"\u{0}"`},
		{"\x7f", `"\u{7f}"`},
		{"\x00\x1f\x7f", `"\u{0}\u{1f}\u{7f}"`},
		{"żółw", `"żółw"`},
		{"🐢", `"🐢"`},
		{"\\́", "\"\\\\́\""}, // a backslash then a combining mark
		// An emoji's joiners, variation selectors, modifiers, regional
		// indicators and tags are part of it and stay as they are.
		{"👩‍💻", "\"👩‍💻\""},
		{"👨‍👩‍👧", "\"👨‍👩‍👧\""},
		{"❤️", "\"❤️\""},
		{"☺︎", "\"☺︎\""},
		{"👍🏽", "\"👍🏽\""},
		{"🇵🇱", "\"🇵🇱\""},
		{"🏴\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F", "\"🏴\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F\""},
		{"é", "\"é\""},
		// A joiner or selector that joins no emoji stays visible.
		{"a‍b", `"a\u{200d}b"`},
		{"‍👩", `"\u{200d}👩"`},
		{"👩‍", `"👩\u{200d}"`},
		{"1️⃣", "\"1️⃣\""}, // a keycap
		{"️", `"\u{fe0f}"`},
		{"a️b", `"a\u{fe0f}b"`},
		{"漢\U000E0100", `"漢\u{e0100}"`},
		// A joiner or non-joiner between two letters or marks of a script
		// is part of the word: a Devanagari conjunct (ZWJ after a virama),
		// a Malayalam chillu inside a word, Persian and Arabic with a ZWNJ.
		{"क्‍ष", "\"क्‍ष\""},
		{"അവന്‍റെ", "\"അവന്‍റെ\""},
		{"می‌خواهم", "\"می‌خواهم\""},
		{"نامه‌ها", "\"نامه‌ها\""},
		{"क‌ष", "\"क‌ष\""},
		// Beside ASCII, a space or punctuation, at either end, or doubled,
		// it stays escaped.
		{"a‌b", `"a\u{200c}b"`},
		{"क‍b", `"क\u{200d}b"`},
		{"‍क", `"\u{200d}क"`},
		{"क्‍", `"क्\u{200d}"`},
		{"ന്‍", `"ന്\u{200d}"`},
		{"क ‍ष", `"क \u{200d}ष"`},
		{"क‍ ष", `"क\u{200d} ष"`},
		{"ی‌.خ", `"ی\u{200c}.خ"`},
		{"क‍‍ष", `"क\u{200d}\u{200d}ष"`},
		{"ی‌‌خ", `"ی\u{200c}\u{200c}خ"`},
		{"USD 5.00", `"USD\u{a0}5.00"`},
		{"3:45 PM", `"3:45\u{202f}PM"`},
		{"\r\n", `"\r\n"`},
		{"", `""`},
	}
	for _, c := range cases {
		if got := StringLiteral(c.in); got != c.want {
			t.Errorf("StringLiteral(%q) = %s, want %s", c.in, got, c.want)
		}
		back, ok := UnquoteStringLiteral(c.want)
		if !ok || back != c.in {
			t.Errorf("UnquoteStringLiteral(%s) = %q, %v, want %q", c.want, back, ok, c.in)
		}
		if got, err := DebugText(c.in, nil); err != nil || got != c.want {
			t.Errorf("DebugText(%q) = %s, %v, want %s", c.in, got, err, c.want)
		}
		if got := RowText(c.in); got != c.want {
			t.Errorf("RowText(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestStringLiteral_BodyLeavesInterpolationOpenersToTheWriter(t *testing.T) {
	if got := StringLiteralBody("a\n${b}"); got != `a\n${b}` {
		t.Errorf("StringLiteralBody = %s", got)
	}
}

func TestUnquoteStringLiteral_RefusesWhatStringLiteralDoesNotWrite(t *testing.T) {
	for _, text := range []string{`ok`, `"`, `"a"b"`, `"a\"`, `"\q"`, `"\$x"`, `"\u{}"`, `"\u{d800}"`, `"\u{110000}"`, `"\u{d"`} {
		if got, ok := UnquoteStringLiteral(text); ok {
			t.Errorf("UnquoteStringLiteral(%s) = %q, want refused", text, got)
		}
	}
}

// Strings inside containers render as the same literal, keys and values.
func TestStringLiteral_InsideContainers(t *testing.T) {
	list := ConsCell[any, List[any]]("x\ny", ConsCell[any, List[any]]("t\tab", nil))
	if got, err := DebugText(list, nil); err != nil || got != `["x\ny", "t\tab"]` {
		t.Errorf("list debug %s %v", got, err)
	}
	if got := RowText(list); got != `["x\ny", "t\tab"]` {
		t.Errorf("list row %s", got)
	}
	m := MapOf(Hash, Equal, []MapEntry[any, any]{{Key: "k\n", Val: "v\t"}})
	if got, err := DebugText(m, nil); err != nil || got != `%{"k\n" => "v\t"}` {
		t.Errorf("map debug %s %v", got, err)
	}
	if got := RowText(m); got != `%{"k\n" => "v\t"}` {
		t.Errorf("map row %s", got)
	}
	tuple := TupleDesc(SlotString, SlotString).Make("a\nb", `${c}`)
	if got, err := DebugText(tuple, nil); err != nil || got != `#("a\nb", "\${c}")` {
		t.Errorf("tuple debug %s %v", got, err)
	}
	if got := Inspect(list).Text(); got != `["x\ny", "t\tab"]` {
		t.Errorf("inspect text %s", got)
	}
	if got := Inspect("a\nb").Text(); got != `"a\nb"` {
		t.Errorf("inspect string %s", got)
	}
}
