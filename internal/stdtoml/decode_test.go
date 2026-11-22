package stdtoml

import (
	"slices"
	"testing"
)

func TestDecodeListsValuesInDocumentOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{"nothing", "", []string{"table", "0"}},
		{"keys in the text's order", "b = 1\na = 'x'\n", []string{
			"table", "2", "b", "int", "1", "a", "string", "x",
		}},
		{"tables and their keys", "z = true\n[t]\ny = 1.5\nx = -2\n[s]\n", []string{
			"table", "3", "z", "bool", "true",
			"t", "table", "2", "y", "float", "1.5", "x", "int", "-2",
			"s", "table", "0",
		}},
		{"dotted keys", "a.c = 1\na.b = 2\n", []string{
			"table", "1", "a", "table", "2", "c", "int", "1", "b", "int", "2",
		}},
		{"an array of tables", "[[p]]\nn = 'a'\nq = 1\n[[p]]\nq = 2\nn = 'b'\n", []string{
			"table", "1", "p", "array", "2",
			"table", "2", "n", "string", "a", "q", "int", "1",
			"table", "2", "n", "string", "b", "q", "int", "2",
		}},
		{"arrays and inline tables", "xs = [1, [2.0], {k = 'v', j = 0}]\n", []string{
			"table", "1", "xs", "array", "3",
			"int", "1", "array", "1", "float", "2.0", "table", "2", "k", "string", "v", "j", "int", "0",
		}},
		{"dates and times", "d = 2026-05-04\nt = 07:32\nl = 1979-05-27 07:32:00.25\no = 1979-05-27T07:32:00Z\np = 1979-05-27T00:32:00-07:00\n", []string{
			"table", "5",
			"d", "date", "2026-05-04",
			"t", "time", "07:32:00",
			"l", "naive", "1979-05-27T07:32:00.25",
			"o", "offset", "1979-05-27T07:32:00Z",
			"p", "offset", "1979-05-27T00:32:00-07:00",
		}},
		{"special floats", "a = inf\nb = -inf\nc = nan\nd = 1e300\n", []string{
			"table", "4", "a", "float", "inf", "b", "float", "-inf", "c", "float", "nan", "d", "float", "1e+300",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodes, offset, _, _, message := HostDecode(tc.text)
			if offset != -1 {
				t.Fatalf("refused at %d: %s", offset, message)
			}
			if !slices.Equal(nodes, tc.want) {
				t.Fatalf("nodes\n %q\nwant\n %q", nodes, tc.want)
			}
		})
	}
}

func TestDecodeRefuses(t *testing.T) {
	for _, tc := range []struct {
		name              string
		text              string
		offset, line, col int64
		message           string
	}{
		{"no value", "title = ", 7, 1, 8, "unexpected EOF; expected value"},
		{"on a later line, after a wide character", "a = 'é'\nb = = 1\n", 12, 2, 5, "expected value but found '=' instead"},
		{"a key twice", "a = 1\na = 2\n", 6, 2, 1, "Key 'a' has already been defined."},
		{"an offset std/calendar cannot hold", "o = 2026-01-01T00:00:00+20:00\n", 0, 1, 1, "o: the offset of 2026-01-01T00:00:00+20:00 is past ±18:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodes, offset, line, col, message := HostDecode(tc.text)
			if nodes != nil || offset != tc.offset || line != tc.line || col != tc.col || message != tc.message {
				t.Fatalf("got %q at %d (%d:%d) %q, want at %d (%d:%d) %q",
					nodes, offset, line, col, message, tc.offset, tc.line, tc.col, tc.message)
			}
		})
	}
}

// A literal's text and std/toml's decode read TOML with one reader, so each
// refuses what the other does, at the same place.
func TestDecodeRefusesWhatALiteralRefuses(t *testing.T) {
	for _, text := range []string{"title = ", "a = = 1", "[t]\n[t]\n", "x = 'é\n"} {
		r := check([]string{text})
		_, offset, _, _, message := HostDecode(text)
		if r == nil || int64(r.offset) != offset || r.message != message {
			t.Errorf("%q: the literal refuses %+v and decode %d %q", text, r, offset, message)
		}
	}
}
