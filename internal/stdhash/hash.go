package stdhash

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	"encoding"
	"fmt"
	"hash"

	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/blake2s"

	"github.com/nomi-language/nomi/rt"
)

// algorithm is one entry of std/hash's `Algorithm`: its variant's name and
// how to make a fresh hash of it.
type algorithm struct {
	name string
	make func() hash.Hash
}

// algorithms is std/hash's `Algorithm` in declaration order: code i is
// variant i (package doc, "Algorithms").
var algorithms = [...]algorithm{
	{"Sha256", sha256.New},
	{"Sha512", sha512.New},
	{"Sha384", sha512.New384},
	{"Sha224", sha256.New224},
	{"Sha512With256", sha512.New512_256},
	{"Sha1", sha1.New},
	{"Md5", md5.New},
	{"Sha3With256", func() hash.Hash { return sha3.New256() }},
	{"Sha3With384", func() hash.Hash { return sha3.New384() }},
	{"Sha3With512", func() hash.Hash { return sha3.New512() }},
	{"Blake2b256", unkeyed(blake2b.New256)},
	{"Blake2b512", unkeyed(blake2b.New512)},
	{"Blake2s256", unkeyed(blake2s.New256)},
}

// unkeyed is a BLAKE2 constructor with no key.
func unkeyed(newKeyed func(key []byte) (hash.Hash, error)) func() hash.Hash {
	return func() hash.Hash {
		h, err := newKeyed(nil)
		if err != nil {
			panic(err) // only a key past the limit fails, and there is none
		}
		return h
	}
}

// maker is the constructor for code, which std/hash.nomi's `code` answers
// for an `Algorithm` and so is always in the table. Every hash it makes is
// a cloning.
func maker(code int64) func() hash.Hash {
	if code < 0 || code >= int64(len(algorithms)) {
		panic(fmt.Sprintf("hash: no algorithm has code %d", code))
	}
	fresh := algorithms[code].make
	return func() hash.Hash { return cloning{fresh(), fresh} }
}

// cloning is a hash as a hash.Cloner, which every hash here is, plain or
// under HMAC. Go's SHA-3 clones, but HMAC over it does not, since HMAC
// clones its inner hashes only when they are FIPS ones it knows that
// clone, and BLAKE2 has no Clone at all. A cloning clones through the
// hash's own Clone when it has one, and otherwise through the state it
// marshals, unmarshaled into a fresh hash; HMAC over a cloning clones
// through it.
type cloning struct {
	hash.Hash
	fresh func() hash.Hash
}

func (c cloning) Clone() (hash.Cloner, error) {
	if cl, ok := c.Hash.(hash.Cloner); ok {
		copied, err := cl.Clone()
		if err != nil {
			return nil, err
		}
		return cloning{copied, c.fresh}, nil
	}
	state, err := c.Hash.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return nil, err
	}
	copied := c.fresh()
	if err := copied.(encoding.BinaryUnmarshaler).UnmarshalBinary(state); err != nil {
		return nil, err
	}
	return cloning{copied, c.fresh}, nil
}

// hmacMaker is the constructor of an HMAC under key over code's hash.
func hmacMaker(code int64, key rt.Bytes) func() hash.Hash {
	inner := maker(code)
	k := []byte(key)
	return func() hash.Hash { return hmac.New(inner, k) }
}

// sum is the digest of data by a fresh hash from make.
func sum(make func() hash.Hash, data rt.Bytes) rt.Bytes {
	h := make()
	h.Write(rt.BytesView(data))
	return rt.Bytes(h.Sum(nil))
}

// Digest is std/hash's `digest`: the digest of data by the algorithm
// whose code is code.
func Digest(code int64, data rt.Bytes) rt.Bytes { return sum(maker(code), data) }

// HMAC is std/hash's `hmac`: the HMAC (RFC 2104) of data under key over
// the algorithm whose code is code.
func HMAC(code int64, key, data rt.Bytes) rt.Bytes { return sum(hmacMaker(code, key), data) }

// HasherNew is std/hash's `Hasher.new`: a Hasher of code's algorithm that
// has read nothing.
func HasherNew(code int64) rt.Hasher { return rt.NewHasher(maker(code)()) }

// HasherNewHMAC is std/hash's `Hasher.new_hmac`: a Hasher of the HMAC under
// key over code's algorithm that has read nothing.
func HasherNewHMAC(code int64, key rt.Bytes) rt.Hasher {
	return rt.NewHasher(hmacMaker(code, key)())
}

// HasherUpdate is `Hasher.update`: h after it reads chunk too. h's own
// state is cloned, never written (package doc, "A Hasher is a value").
func HasherUpdate(h rt.Hasher, chunk rt.Bytes) rt.Hasher {
	c := clone(h.State())
	c.Write(rt.BytesView(chunk))
	return rt.NewHasher(c)
}

// HasherFinish is `Hasher.finish`: the digest or MAC of what h has read.
func HasherFinish(h rt.Hasher) rt.Bytes {
	return rt.Bytes(clone(h.State()).Sum(nil))
}

// clone is a copy of h's state that writing to leaves h as it is.
func clone(h hash.Hash) hash.Hash {
	c, err := h.(hash.Cloner).Clone()
	if err != nil {
		panic(fmt.Sprintf("hash: a state does not clone: %v", err))
	}
	return c
}

// HKDF is std/hash's `hkdf`: RFC 5869's HKDF with the HMAC over code's
// algorithm, over secret, salt and info, length bytes long, or why that
// length is past HKDF's limit of 255 blocks of the digest's size.
func HKDF(code int64, secret, salt, info rt.Bytes, length rt.Int) rt.Result[rt.Bytes, string] {
	n, err := hkdfLength(code, length)
	if err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	key, err := hkdf.Key(maker(code), []byte(secret), []byte(salt), string(info), n)
	if err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	return rt.Ok[rt.Bytes, string](rt.Bytes(key))
}

// HKDFExtract is std/hash's `hkdf_extract`: HKDF's extract step, the
// pseudorandom key of secret and salt, as long as code's digest.
func HKDFExtract(code int64, secret, salt rt.Bytes) rt.Bytes {
	key, err := hkdf.Extract(maker(code), []byte(secret), []byte(salt))
	if err != nil {
		panic("hash.hkdf_extract: " + err.Error()) // only FIPS 140-only mode fails, and it is off
	}
	return rt.Bytes(key)
}

// HKDFExpand is std/hash's `hkdf_expand`: HKDF's expand step, length bytes
// from the pseudorandom key and info, or why length is past the limit.
func HKDFExpand(code int64, key, info rt.Bytes, length rt.Int) rt.Result[rt.Bytes, string] {
	n, err := hkdfLength(code, length)
	if err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	out, err := hkdf.Expand(maker(code), []byte(key), string(info), n)
	if err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	return rt.Ok[rt.Bytes, string](rt.Bytes(out))
}

// hkdfLength is length as the int crypto/hkdf takes, or why it is past 255
// blocks of code's digest. std declares length a PositiveInt of any size,
// and one past int64 is past the limit too.
func hkdfLength(code int64, length rt.Int) (int, error) {
	most := 255 * maker(code)().Size()
	if n := length.Saturated(); n <= int64(most) {
		return int(n), nil
	}
	return 0, fmt.Errorf("an HKDF key over %s is at most %d bytes, got %s", algorithms[code].name, most, length)
}

// Shake128 is std/hash's `shake128`: length bytes of SHAKE128 (FIPS 202)
// output over data.
func Shake128(data rt.Bytes, length rt.Int) rt.Bytes {
	return shake(sha3.NewSHAKE128(), "hash.shake128", data, length)
}

// Shake256 is std/hash's `shake256`: length bytes of SHAKE256 (FIPS 202)
// output over data.
func Shake256(data rt.Bytes, length rt.Int) rt.Bytes {
	return shake(sha3.NewSHAKE256(), "hash.shake256", data, length)
}

func shake(x *sha3.SHAKE, fn string, data rt.Bytes, length rt.Int) rt.Bytes {
	out := make([]byte, rt.ByteCount(fn, length))
	x.Write(rt.BytesView(data))
	x.Read(out)
	return rt.Bytes(out)
}
