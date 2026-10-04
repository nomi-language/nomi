package wasmmem

import (
	"bytes"
	"strings"
	"testing"
)

// module builds magic + a type section + a memory section with the given
// limits bytes + a trailing custom section, so the rewrite has neighbours on
// both sides to preserve.
func module(limits ...byte) []byte {
	m := append([]byte{}, magic...)
	m = append(m, 1, 1, 0) // type section: zero types
	body := append([]byte{1}, limits...)
	m = append(m, 5, byte(len(body)))
	m = append(m, body...)
	m = append(m, 0, 3, 1, 'x', 9) // custom section "x" with one byte
	return m
}

func TestSetMax_AddsAMaximum(t *testing.T) {
	// Go's linker writes a padded LEB128 initial size; 0x82 0x80 0x00 is 2.
	got, err := SetMax(module(0, 0x82, 0x80, 0x00), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	want := module(1, 2, 0x80, 0x80, 0x01) // max 16384 pages
	if !bytes.Equal(got, want) {
		t.Fatalf("SetMax =\n%x\nwant\n%x", got, want)
	}
}

func TestSetMax_ReplacesAMaximum(t *testing.T) {
	got, err := SetMax(module(1, 2, 0xff, 0xff, 0x03), 128<<10)
	if err != nil {
		t.Fatal(err)
	}
	if want := module(1, 2, 2); !bytes.Equal(got, want) {
		t.Fatalf("SetMax =\n%x\nwant\n%x", got, want)
	}
}

func TestSetMax_Refuses(t *testing.T) {
	for name, c := range map[string]struct {
		wasm []byte
		want string
	}{
		"initial above max": {module(0, 20), "above"},
		"no memory":         {append(append([]byte{}, magic...), 1, 1, 0), "no memory"},
		"not wasm":          {[]byte("hello"), "not a wasm"},
	} {
		if _, err := SetMax(c.wasm, 64<<10); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one containing %q", name, err, c.want)
		}
	}
}
