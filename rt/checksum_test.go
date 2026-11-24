package rt

import (
	"math/big"
	"testing"
)

// checksums is each std/checksum update function with the value of no
// bytes, its whole form's start, and the standard check value: the checksum
// of the ASCII digits "123456789" (the CRC catalogue's "check" for each CRC,
// RFC 1950's Adler-32, and FNV-1a by its definition).
var checksums = []struct {
	name   string
	update func(Int, Bytes) Int
	start  uint64
	check  uint64
}{
	{"crc32", func(c Int, d Bytes) Int { return SmallInt(Crc32Update(c, d)) }, 0, 0xCBF43926},
	{"crc32c", func(c Int, d Bytes) Int { return SmallInt(Crc32cUpdate(c, d)) }, 0, 0xE3069283},
	{"crc64_iso", Crc64IsoUpdate, 0, 0xB90956C775A41001},
	{"crc64_ecma", Crc64EcmaUpdate, 0, 0x995DC9BBDF1939FA},
	{"adler32", func(c Int, d Bytes) Int { return SmallInt(Adler32Update(c, d)) }, 1, 0x091E01DE},
	{"fnv1a_32", func(c Int, d Bytes) Int { return SmallInt(Fnv1a32Update(c, d)) }, 0x811C9DC5, 0xBB86B11C},
	{"fnv1a_64", Fnv1a64Update, 0xCBF29CE484222325, 0x06D5573923C6CDFC},
}

func TestChecksums_CheckValues(t *testing.T) {
	for _, c := range checksums {
		if got := c.update(UintInt(c.start), "123456789"); !sameInt(got, UintInt(c.check)) {
			t.Errorf("%s(\"123456789\") = %s, want %d", c.name, got, c.check)
		}
		if got := c.update(UintInt(c.start), ""); !sameInt(got, UintInt(c.start)) {
			t.Errorf("%s of no bytes = %s, want its start %d", c.name, got, c.start)
		}
	}
}

// Continuing from the value of a prefix gives the value of the whole, at
// every split, and a running value past the checksum's width is read by
// its low bits.
func TestChecksums_UpdateContinues(t *testing.T) {
	data := Bytes("The quick brown fox jumps over the lazy dog")
	for _, c := range checksums {
		whole := c.update(UintInt(c.start), data)
		for i := 0; i <= len(data); i++ {
			if got := c.update(c.update(UintInt(c.start), data[:i]), data[i:]); !sameInt(got, whole) {
				t.Errorf("%s split at %d = %s, want %s", c.name, i, got, whole)
			}
		}
		wider := new(big.Int).Lsh(big.NewInt(1), 64)
		wider.Add(wider, new(big.Int).SetUint64(c.start))
		if got := c.update(BigIntOf(wider), data); !sameInt(got, whole) {
			t.Errorf("%s from 2^64 + its start = %s, want %s", c.name, got, whole)
		}
	}
}

func sameInt(a, b Int) bool { return a.Big().Cmp(b.Big()) == 0 }
