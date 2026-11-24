package rt

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBytesXor(t *testing.T) {
	if got := BytesXor("\x0f\xf0\xaa", "\xff\xff\x0a"); got != Some(Bytes("\xf0\x0f\xa0")) {
		t.Errorf("xor = %v", got)
	}
	if got := BytesXor("", ""); got != Some(Bytes("")) {
		t.Errorf("xor of two empty = %v", got)
	}
	if got := BytesXor("ab", "a"); got != None[Bytes]() {
		t.Errorf("xor of different lengths = %v, want None", got)
	}
}

// XorRepeating XORs each byte with the key's byte at its index modulo the
// key's length, from the phase given, for keys shorter and longer than its
// stretch and data shorter and longer than it, written in place too.
func TestXorRepeating_IsTheKeyRepeated(t *testing.T) {
	data := make([]byte, 1500)
	for i := range data {
		data[i] = byte(i * 31)
	}
	for _, n := range []int{1, 3, 4, 255, 256, 257, 700, 2000} {
		key := make([]byte, n)
		for i := range key {
			key[i] = byte(i*7 + 1)
		}
		for _, size := range []int{0, 1, 5, 256, 1500} {
			for _, phase := range []int{0, 1, n - 1} {
				want := make([]byte, size)
				for i := range want {
					want[i] = data[i] ^ key[(phase+i)%n]
				}
				got := make([]byte, size)
				XorRepeating(got, data[:size], key, phase)
				if string(got) != string(want) {
					t.Errorf("key %d bytes, data %d bytes, phase %d: wrong bytes", n, size, phase)
				}
				inPlace := append([]byte(nil), data[:size]...)
				XorRepeating(inPlace, inPlace, key, phase)
				if string(inPlace) != string(want) {
					t.Errorf("key %d bytes, data %d bytes, phase %d, in place: wrong bytes", n, size, phase)
				}
			}
		}
	}
}

func TestBytesXorRepeating(t *testing.T) {
	// RFC 6455, section 5.7: "Hello" masked with 0x37fa213d.
	if got := BytesXorRepeating("Hello", "\x37\xfa\x21\x3d"); got != "\x7f\x9f\x4d\x51\x58" {
		t.Errorf("masked = %q", got)
	}
	if got := BytesXorRepeating("Hello", ""); got != "Hello" {
		t.Errorf("an empty key gives %q, want the data", got)
	}
}

func TestBytesFillAndReverse(t *testing.T) {
	if got := BytesFill(SmallInt(4), 0x2a); got != "****" {
		t.Errorf("fill = %q", got)
	}
	if got := BytesFill(SmallInt(3), 0); got != "\x00\x00\x00" {
		t.Errorf("fill with 0 = %q", got)
	}
	if got := BytesFill(SmallInt(0), 7); got != "" {
		t.Errorf("fill 0 = %q", got)
	}
	if got := BytesReverse("abc"); got != "cba" {
		t.Errorf("reverse = %q", got)
	}
	if got := BytesReverse(""); got != "" {
		t.Errorf("reverse of empty = %q", got)
	}
}

func TestBytesFill_ALengthPastMemoryTraps(t *testing.T) {
	defer func() {
		e, ok := recover().(*Error)
		if !ok || !strings.Contains(e.Msg, "Bytes.fill: 4294967296 bytes are more than memory holds") {
			t.Errorf("recovered %v, want a trap naming Bytes.fill and the length", e)
		}
	}()
	BytesFill(SmallInt(1<<32), 1)
	t.Fatal("no trap")
}

// Each offset of a Bytes decodes as Go's UTF-8 decoder reads it, and an
// offset where Go reads an ill-formed sequence (RuneError of width 1) is
// None.
func TestBytesCodePointAt(t *testing.T) {
	data := Bytes("aé€😀\xff\xc0\xaf\xed\xa0\x80\xf4\x90\x80\x80\xe2\x82")
	for i := -1; i <= len(data); i++ {
		got := BytesCodePointAt(data, SmallInt(int64(i)))
		if i < 0 || i >= len(data) {
			if got.Tag == 1 {
				t.Errorf("offset %d outside: %v", i, got)
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(string(data[i:]))
		if r == utf8.RuneError && size == 1 {
			if got.Tag == 1 {
				t.Errorf("offset %d is ill-formed and decodes as %v", i, got)
			}
			continue
		}
		if !(got.Tag == 1) || got.Some.F0 != Codepoint(r) || got.Some.F1 != int64(size) {
			t.Errorf("offset %d = %v, want (%U, %d)", i, got, r, size)
		}
	}
	if got := BytesCodePointAt("\xef\xbf\xbd", SmallInt(0)); !(got.Tag == 1) || got.Some.F0 != 0xFFFD || got.Some.F1 != 3 {
		t.Errorf("U+FFFD itself = %v, want it with 3 bytes", got)
	}
}
