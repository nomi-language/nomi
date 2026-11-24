package rt

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math/bits"
	"os"
	"testing"
)

// refSipHash is SipHash-c-d written from the paper, a byte at a time, for
// checking sipHash13: it reproduces the reference vectors at c=2, d=4.
func refSipHash(k0, k1 uint64, msg []byte, c, d int) uint64 {
	v := [4]uint64{k0 ^ 0x736f6d6570736575, k1 ^ 0x646f72616e646f6d, k0 ^ 0x6c7967656e657261, k1 ^ 0x7465646279746573}
	round := func() {
		v[0] += v[1]
		v[1] = bits.RotateLeft64(v[1], 13) ^ v[0]
		v[0] = bits.RotateLeft64(v[0], 32)
		v[2] += v[3]
		v[3] = bits.RotateLeft64(v[3], 16) ^ v[2]
		v[0] += v[3]
		v[3] = bits.RotateLeft64(v[3], 21) ^ v[0]
		v[2] += v[1]
		v[1] = bits.RotateLeft64(v[1], 17) ^ v[2]
		v[2] = bits.RotateLeft64(v[2], 32)
	}
	padded := append(append([]byte{}, msg...), make([]byte, 8-len(msg)%8)...)
	padded[len(padded)-1] = byte(len(msg))
	for i := 0; i < len(padded); i += 8 {
		m := binary.LittleEndian.Uint64(padded[i:])
		v[3] ^= m
		for range c {
			round()
		}
		v[0] ^= m
	}
	v[2] ^= 0xff
	for range d {
		round()
	}
	return v[0] ^ v[1] ^ v[2] ^ v[3]
}

// sipHash13 is SipHash-1-3: the reference, which gives the paper's
// vectors at 2-4, agrees with it at 1-3 for every length across two
// blocks, under two keys.
func TestSipHash13IsTheReference(t *testing.T) {
	k0, k1 := uint64(0x0706050403020100), uint64(0x0f0e0d0c0b0a0908)
	for n, want := range []uint64{0x726fdb47dd0e0e31, 0x74f839c593dc67fd} {
		msg := make([]byte, n)
		for i := range msg {
			msg[i] = byte(i)
		}
		if got := refSipHash(k0, k1, msg, 2, 4); got != want {
			t.Fatalf("the reference SipHash-2-4 of %d bytes is %#x, want the paper's %#x", n, got, want)
		}
	}
	checked := 0
	for _, key := range [][2]uint64{{k0, k1}, {0x9e3779b97f4a7c15, 0xdeadbeefcafef00d}} {
		for n := 0; n <= 24; n++ {
			msg := make([]byte, n)
			for i := range msg {
				msg[i] = byte(31*i + n)
			}
			if got, want := sipHash13(key[0], key[1], string(msg)), refSipHash(key[0], key[1], msg, 1, 3); got != want {
				t.Errorf("sipHash13 of %d bytes under %#x is %#x, the reference %#x", n, key, got, want)
			}
			checked++
		}
	}
	t.Logf("%d messages", checked)
}

// withHashKey runs f under the key keysFromSeed makes of (s0, s1), and
// restores the process's key after.
func withHashKey(t *testing.T, s0, s1 uint64, f func()) {
	t.Helper()
	saved := hashKey
	defer setHashKey(saved)
	setHashKey(keysFromSeed(s0, s1))
	f()
}

// randomSeed is 128 bits from crypto/rand, as a process draws its own.
func randomSeed() (uint64, uint64) {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:8]), binary.LittleEndian.Uint64(b[8:])
}

// A test binary fixes its seed, and hands it to every process it starts
// through the environment.
func TestHashSeed_ATestBinaryFixesItAndHandsItDown(t *testing.T) {
	if got := os.Getenv(hashSeedEnv); got != testHashSeed {
		t.Fatalf("%s is %q in a test binary, want %q", hashSeedEnv, got, testHashSeed)
	}
	if want := keysFromSeed(sipHash13(0, 0, testHashSeed), sipHash13(0, 1, testHashSeed)); hashKey != want {
		t.Fatalf("the key is %+v, want the one the fixed seed gives, %+v", hashKey, want)
	}
}

// hashedValues are values of every structural kind, each built twice so
// that equal values are distinct Go values: a String, a Bytes, an Int past
// int64, a Decimal, a List, a Vector, a tuple, a struct, an enum variant
// with fields, a distinct, a Map and a Set's backing map.
func hashedValues(i int) []any {
	point := NewStructDesc("geo.Point", []FieldSpec{{"x", SlotInt}, {"y", SlotInt}})
	shape := NewEnumDesc("geo.Shape", []VariantSpec{{Name: "Rect", Shape: VariantFields, Fields: []FieldSpec{{"w", SlotInt}, {"h", SlotString}}}})
	meters := NewDistinctDesc("geo.Meters", &[]SlotType{SlotInt}[0])
	big, _ := ParseIntValue(fmt.Sprintf("1%040d", i))
	name := fmt.Sprintf("name-%d", i)
	m := MapOf(Hash, Equal, []MapEntry[any, any]{{Key: name, Val: int64(i)}, {Key: int64(i), Val: name}})
	return []any{
		name,
		Bytes(name),
		big,
		Cons[any](int64(i), Cons[any](name, nil)),
		VectorOf[any]([]any{name, int64(i)}),
		TupleDesc(SlotInt, SlotString).Make(int64(i), name),
		point.Make(int64(i), int64(-i)),
		shape.MakeVariant(0, int64(i), name),
		meters.Make(int64(i)),
		m,
	}
}

// Two seeds give different hashes and the same program: under each, equal
// values hash alike, a Map built of every kind finds each key again, and it
// iterates in insertion order.
func TestHashSeed_TwoSeedsGiveDifferentHashesAndOneBehavior(t *testing.T) {
	type run struct {
		hashes []uint64
		order  string
	}
	under := func(s0, s1 uint64) run {
		var r run
		withHashKey(t, s0, s1, func() {
			a, b := hashedValues(7), hashedValues(7)
			var m Map[any, any]
			for i := range a {
				if ha, hb := Hash(a[i]), Hash(b[i]); ha != hb {
					t.Fatalf("value %d (%T): two equal values hash %#x and %#x", i, a[i], ha, hb)
				}
				r.hashes = append(r.hashes, Hash(a[i]))
				m = MapPut(m, Hash, Equal, a[i], any(int64(i)))
			}
			for i := range b {
				if v, ok := MapLookup(m, Hash, Equal, b[i]); !ok || v != any(int64(i)) {
					t.Fatalf("value %d (%T): the map answers (%v, %v), want (%d, true)", i, b[i], v, ok, i)
				}
			}
			for _, e := range MapEntries(m) {
				r.order += fmt.Sprint(e.Val, " ")
			}
		})
		return r
	}
	s0, s1 := randomSeed()
	one, two := under(1, 2), under(s0, s1)
	if one.order != two.order {
		t.Fatalf("the map iterates %q under one seed and %q under another", one.order, two.order)
	}
	if want := "0 1 2 3 4 5 6 7 8 9 "; one.order != want {
		t.Fatalf("the map iterates %q, want insertion order %q", one.order, want)
	}
	same := 0
	for i := range one.hashes {
		if one.hashes[i] == two.hashes[i] {
			same++
			t.Errorf("value %d (%T) hashes %#x under both seeds", i, hashedValues(0)[i], one.hashes[i])
		}
	}
	t.Logf("%d values, %d hash alike under two seeds", len(one.hashes), same)
}

// The parts of a struct, a tuple, a List and a Vector cannot trade places
// unseen: `Point{x: 1, y: 2}` and `Point{x: 2, y: 1}` hash apart, and so
// do `(1, 2)` and `(2, 1)`, `[1, 2]` and `[2, 1]`. A struct's two records
// of one value hash alike whatever their layouts' field order.
func TestHash_SwappedPartsHashApart(t *testing.T) {
	point := NewStructDesc("geo.Point", []FieldSpec{{"x", SlotInt}, {"y", SlotInt}})
	swapped := NewStructDesc("geo.Point", []FieldSpec{{"y", SlotInt}, {"x", SlotInt}})
	pair := TupleDesc(SlotInt, SlotInt)
	listOf := func(a, b int64) any { return Cons[any](a, Cons[any](b, nil)) }
	pairs := 0
	for _, seed := range [][2]uint64{{1, 2}, {0x5eed, 0xf00d}} {
		withHashKey(t, seed[0], seed[1], func() {
			for _, c := range []struct {
				what       string
				a, b, same any
			}{
				{"a struct", point.Make(int64(1), int64(2)), point.Make(int64(2), int64(1)), swapped.Make(int64(2), int64(1))},
				{"a tuple", pair.Make(int64(1), int64(2)), pair.Make(int64(2), int64(1)), nil},
				{"a List", listOf(1, 2), listOf(2, 1), nil},
				{"a Vector", VectorOf[any]([]any{int64(1), int64(2)}), VectorOf[any]([]any{int64(2), int64(1)}), nil},
			} {
				pairs++
				if Hash(c.a) == Hash(c.b) {
					t.Errorf("%s with its two parts swapped hashes alike, %#x", c.what, Hash(c.a))
				}
				if c.same != nil && Hash(c.same) != Hash(c.a) {
					t.Errorf("%s laid out in another field order hashes %#x, not %#x", c.what, Hash(c.same), Hash(c.a))
				}
			}
		})
	}
	t.Logf("%d pairs", pairs)
}

// collidingPairs are pairs of 11-character strings found by searching
// FNV-1a 64: from the state the previous pairs leave, each pair's two
// strings reach one state, so every string made of one string from each
// pair, in order, has one FNV-1a hash.
var collidingPairs = [][2]string{
	{"7UJ11UOLvvF", "nlPV-ow8BSJ"},
	{"gw4M9OcDRAM", "8qSbnMuSPJC"},
	{"6_I3LT98utM", "wvV-zYhb2HK"},
	{"XVTwttVnsFE", "I023KJZqtFO"},
	{"jo9v4IwyIcE", "FSlEiYAJqjL"},
	{"XbiZIGo-ENI", "P3x9ePB0qpK"},
	{"xD7v4oWDKIH", "uJluIM2JlyJ"},
	{"4xmuYjU3mND", "MotFFe6cC1F"},
	{"dKvZsBQa_HA", "vRwDE5wwzHM"},
	{"1gFAyaQMo2J", "n79R-zv38EA"},
}

func fnv1a(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// The 1,024 strings the colliding pairs make share one FNV-1a hash, and
// under a random seed, as every process draws one, each has a hash of its
// own: the count of distinct hashes is the count of strings.
func TestHashString_FNVCollisionsDoNotCollide(t *testing.T) {
	keys := []string{""}
	for _, p := range collidingPairs {
		var next []string
		for _, k := range keys {
			next = append(next, k+p[0], k+p[1])
		}
		keys = next
	}
	fnvs := map[uint64]bool{}
	for _, k := range keys {
		fnvs[fnv1a(k)] = true
	}
	if len(fnvs) != 1 {
		t.Fatalf("the %d strings have %d FNV-1a hashes, so the pairs are not the collisions this test is about", len(keys), len(fnvs))
	}
	s0, s1 := randomSeed()
	withHashKey(t, s0, s1, func() {
		hashes := map[uint64]bool{}
		for _, k := range keys {
			hashes[HashString(k)] = true
		}
		if len(hashes) != len(keys) {
			t.Fatalf("the %d strings have %d distinct hashes under a random seed", len(keys), len(hashes))
		}
	})
	t.Logf("%d strings: 1 FNV-1a hash, %d keyed hashes", len(keys), len(keys))
}
