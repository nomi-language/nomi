package rt

import (
	"strings"
	"testing"
)

func TestSealOpensOnlyUnderItsKeyAndLabel(t *testing.T) {
	key := Bytes(strings.Repeat("k", SealKeySize))
	sealed := Seal(key, Bytes("hello"), Bytes("label"))
	if sealed.Tag != TagOk {
		t.Fatalf("Seal: %v", sealed.Err)
	}
	if got := Open(key, sealed.Ok, Bytes("label")); got.Tag != TagSome || got.Some != Bytes("hello") {
		t.Fatalf("Open = %+v, want Some(hello)", got)
	}
	again := Seal(key, Bytes("hello"), Bytes("label"))
	if again.Ok == sealed.Ok {
		t.Fatal("two seals of one value are equal; the nonce is not fresh")
	}

	flipped := []byte(sealed.Ok)
	flipped[len(flipped)/2] ^= 1
	refused := map[string]Maybe[Bytes]{
		"another key":   Open(Bytes(strings.Repeat("j", SealKeySize)), sealed.Ok, Bytes("label")),
		"another label": Open(key, sealed.Ok, Bytes("other")),
		"a changed bit": Open(key, Bytes(flipped), Bytes("label")),
		"too short":     Open(key, sealed.Ok[:20], Bytes("label")),
		"a short key":   Open(Bytes("short"), sealed.Ok, Bytes("label")),
	}
	for name, got := range refused {
		if got.Tag != TagNone {
			t.Errorf("%s: Open = %+v, want None", name, got)
		}
	}

	if short := Seal(Bytes("short"), Bytes("x"), Bytes("")); short.Tag != TagErr {
		t.Errorf("Seal under a 5-byte key = %+v, want Err", short)
	}
}
