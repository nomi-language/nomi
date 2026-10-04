package strlit

import "testing"

func TestDecodeUnicodeEscape(t *testing.T) {
	valid := map[string]rune{"0": 0, "41": 'A', "000041": 'A', "e9": 'é', "1F600": '😀', "10FFFF": 0x10FFFF, "D7FF": 0xD7FF, "E000": 0xE000}
	for hex, want := range valid {
		if got, problem := DecodeUnicodeEscape(hex); problem != "" || got != want {
			t.Errorf("DecodeUnicodeEscape(%q) = %U, %q; want %U", hex, got, problem, want)
		}
	}
	invalid := map[string]string{
		"":         `invalid \u{...} code point: ; a \u{...} escape takes 1 to 6 hex digits`,
		"0000041":  `invalid \u{...} code point: 0000041; a \u{...} escape takes 1 to 6 hex digits`,
		"-41":      `invalid \u{...} code point: -41; a \u{...} escape takes 1 to 6 hex digits`,
		"g":        `invalid \u{...} code point: g; a \u{...} escape takes 1 to 6 hex digits`,
		"110000":   `invalid \u{...} code point: 110000; the largest code point is 10FFFF`,
		"FFFFFF":   `invalid \u{...} code point: FFFFFF; the largest code point is 10FFFF`,
		"D800":     `invalid \u{...} code point: D800; D800 to DFFF are surrogates, not Unicode scalar values`,
		"dfff":     `invalid \u{...} code point: dfff; D800 to DFFF are surrogates, not Unicode scalar values`,
		"12345678": `invalid \u{...} code point: 12345678; a \u{...} escape takes 1 to 6 hex digits`,
	}
	for hex, want := range invalid {
		if _, problem := DecodeUnicodeEscape(hex); problem != want {
			t.Errorf("DecodeUnicodeEscape(%q) problem:\n got %q\nwant %q", hex, problem, want)
		}
	}
}

func TestEncodeStringBody_PrintablePassThrough(t *testing.T) {
	// Printable runes — ASCII, accented letters, emoji — pass through verbatim.
	in := "héllo \U0001F600"
	if got := EncodeStringBody(in); got != in {
		t.Errorf("printable runes should pass through: got %q, want %q", got, in)
	}
}

func TestEncodeStringBody_SimpleEscapes(t *testing.T) {
	if got := EncodeStringBody("a\tb\nc\\d\"e"); got != `a\tb\nc\\d\"e` {
		t.Errorf("got %q", got)
	}
}

func TestEncodeStringBody_NonPrintableUsesBracedHex(t *testing.T) {
	if got := EncodeStringBody("\u200d"); got != `\u{200d}` { // ZERO WIDTH JOINER
		t.Errorf("ZWJ: got %q, want \\u{200d}", got)
	}
	if got := EncodeStringBody("\a"); got != `\u{7}` { // BELL
		t.Errorf("BELL: got %q, want \\u{7}", got)
	}
}

// The whole point of the package: decode and encode share one table, so every
// simple escape round-trips. This is what strconv.Quote (Go's table) failed.
func TestSimpleEscapes_DecodeIsInverseOfEncode(t *testing.T) {
	for _, r := range []rune{'\n', '\t', '\\', '"'} {
		enc := EncodeStringBody(string(r)) // e.g. `\n`
		if len(enc) != 2 || enc[0] != '\\' {
			t.Fatalf("expected a 2-char escape for %q, got %q", r, enc)
		}
		dec, ok := DecodeSimple(enc[1])
		if !ok || dec != r {
			t.Errorf("round-trip failed for %q: enc=%q dec=%q ok=%v", r, enc, dec, ok)
		}
	}
}

func TestDecodeSimple_RejectsUnknown(t *testing.T) {
	if _, ok := DecodeSimple('q'); ok {
		t.Error(`\q should not be a recognized simple escape`)
	}
}
