package rt

import (
	"encoding"
	"encoding/binary"
	"hash"
	"hash/adler32"
	"hash/crc32"
	"hash/crc64"
	"hash/fnv"
)

// std/checksum: the checksums formats and protocols carry, over Go's
// hash/crc32 (which uses the processor's CRC instructions where it has
// them), hash/crc64, hash/adler32 and hash/fnv.
//
// Each checksum has a whole form and an `_update` form that takes the
// value so far first, so a reader checksums a stream as it reads it with
// no handle: update(whole(a), b) is whole(a ++ b), and whole(data) is
// update(whole(empty), data). A value is the checksum as its format writes
// it, never a register in some other form, so a running value can be
// compared with one a file holds. std declares the value an Int; a
// running value takes its low 32 or 64 bits, as std/int's wrapping
// operations read an operand.
//
// The CRC tables are Go's, which crc32.MakeTable and crc64.MakeTable build
// once, on the first call that names their polynomial, and answer from
// then on. So each function asks for its table when it runs, and a
// program that never computes a CRC-32C or CRC-64 never builds one (41
// KiB of heap for the three on wasm).

// Crc32Update is `checksum.crc32_update`: CRC-32 (IEEE 802.3, as zip,
// gzip and PNG use) continued over data.
func Crc32Update(crc Int, data Bytes) int64 {
	return int64(crc32.Update(uint32(crc.Low64()), crc32.IEEETable, BytesView(data)))
}

// Crc32cUpdate is `checksum.crc32c_update`: CRC-32C (Castagnoli, as iSCSI,
// ext4 and Kafka use) continued over data.
func Crc32cUpdate(crc Int, data Bytes) int64 {
	return int64(crc32.Update(uint32(crc.Low64()), crc32.MakeTable(crc32.Castagnoli), BytesView(data)))
}

// Crc64IsoUpdate is `checksum.crc64_iso_update`: CRC-64 with the ISO 3309
// polynomial continued over data.
func Crc64IsoUpdate(crc Int, data Bytes) Int {
	return UintInt(crc64.Update(crc.Low64(), crc64.MakeTable(crc64.ISO), BytesView(data)))
}

// Crc64EcmaUpdate is `checksum.crc64_ecma_update`: CRC-64 with the ECMA-182
// polynomial continued over data.
func Crc64EcmaUpdate(crc Int, data Bytes) Int {
	return UintInt(crc64.Update(crc.Low64(), crc64.MakeTable(crc64.ECMA), BytesView(data)))
}

// Adler32Update is `checksum.adler32_update`: Adler-32 (RFC 1950, as zlib
// uses) continued over data.
func Adler32Update(sum Int, data Bytes) int64 {
	return int64(resumed(adler32.New(), sum.Low64()&0xFFFFFFFF, data).(hash.Hash32).Sum32())
}

// Fnv1a32Update is `checksum.fnv1a_32_update`: 32-bit FNV-1a continued over
// data.
func Fnv1a32Update(sum Int, data Bytes) int64 {
	return int64(resumed(fnv.New32a(), sum.Low64()&0xFFFFFFFF, data).(hash.Hash32).Sum32())
}

// Fnv1a64Update is `checksum.fnv1a_64_update`: 64-bit FNV-1a continued over
// data.
func Fnv1a64Update(sum Int, data Bytes) Int {
	return UintInt(resumed(fnv.New64a(), sum.Low64(), data).(hash.Hash64).Sum64())
}

// resumed is h, a fresh Adler-32 or FNV-1a, set to the value so far and
// then fed data. Neither package can start from a value except through
// its saved state, which is a header and the value big-endian in the
// state's last 4 or 8 bytes.
func resumed(h hash.Hash, value uint64, data Bytes) hash.Hash {
	state, err := h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		panic(err)
	}
	if h.Size() == 4 {
		binary.BigEndian.PutUint32(state[len(state)-4:], uint32(value))
	} else {
		binary.BigEndian.PutUint64(state[len(state)-8:], value)
	}
	if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(state); err != nil {
		panic(err)
	}
	h.Write(BytesView(data))
	return h
}
