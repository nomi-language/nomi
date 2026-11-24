package stdhash

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// codeOf is the code of the algorithm named name.
func codeOf(t *testing.T, name string) int64 {
	t.Helper()
	for i, a := range algorithms {
		if a.name == name {
			return int64(i)
		}
	}
	t.Fatalf("no algorithm is named %s", name)
	return -1
}

// The "abc" and empty-input examples of FIPS 180-4 (SHA-1 and SHA-2),
// FIPS 202 (SHA-3), RFC 1321 (MD5) and RFC 7693 (BLAKE2), for every
// algorithm the table holds.
func TestDigest_KnownAnswers(t *testing.T) {
	want := map[string][2]string{ // "abc", then ""
		"Sha256":        {"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		"Sha512":        {"ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f", "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e"},
		"Sha384":        {"cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7", "38b060a751ac96384cd9327eb1b1e36a21fdb71114be07434c0cc7bf63f6e1da274edebfe76f65fbd51ad2f14898b95b"},
		"Sha224":        {"23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7", "d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f"},
		"Sha512With256": {"53048e2681941ef99b2e29b76b4c7dabe4c2d0c634fc6d46e0e2f13107e7af23", "c672b8d1ef56ed28ab87c3622c5114069bdd3ad7b8f9737498d0c01ecef0967a"},
		"Sha1":          {"a9993e364706816aba3e25717850c26c9cd0d89d", "da39a3ee5e6b4b0d3255bfef95601890afd80709"},
		"Md5":           {"900150983cd24fb0d6963f7d28e17f72", "d41d8cd98f00b204e9800998ecf8427e"},
		"Sha3With256":   {"3a985da74fe225b2045c172d6bd390bd855f086e3e9d525b46bfe24511431532", "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
		"Sha3With384":   {"ec01498288516fc926459f58e2c6ad8df9b473cb0fc08c2596da7cf0e49be4b298d88cea927ac7f539f1edf228376d25", "0c63a75b845e4f7d01107d852e4c2485c51a50aaaa94fc61995e71bbee983a2ac3713831264adb47fb6bd1e058d5f004"},
		"Sha3With512":   {"b751850b1a57168a5693cd924b6b096e08f621827444f70d884f5d0240d2712e10e116e9192af3c91a7ec57647e3934057340b4cf408d5a56592f8274eec53f0", "a69f73cca23a9ac5c8b567dc185a756e97c982164fe25859e0d1dcc1475c80a615b2123af1f5f94c11e3e9402c3ac558f500199d95b6d3e301758586281dcd26"},
		"Blake2b256":    {"bddd813c634239723171ef3fee98579b94964e3bb1cb3e427262c8c068d52319", "0e5751c026e543b2e8ab2eb06099daa1d1e5df47778f7787faab45cdf12fe3a8"},
		"Blake2b512":    {"ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d17d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923", "786a02f742015903c6c6fd852552d272912f4740e15847618a86e217f71f5419d25e1031afee585313896444934eb04b903a685b1448b755d56f701afe9be2ce"},
		"Blake2s256":    {"508c5e8c327c14e2e1a72ba34eeb452f37458b209ed63a294d999b4c86675982", "69217a3079908094e11121d042354a7c1f55b6482ca1a51e1b250dfd1ed0eef9"},
	}
	if len(want) != len(algorithms) {
		t.Fatalf("%d known answers for %d algorithms", len(want), len(algorithms))
	}
	for i, a := range algorithms {
		w, ok := want[a.name]
		if !ok {
			t.Errorf("no known answer for %s", a.name)
			continue
		}
		for j, in := range []string{"abc", ""} {
			if got := hex.EncodeToString([]byte(Digest(int64(i), rt.Bytes(in)))); got != w[j] {
				t.Errorf("%s(%q) = %s, want %s", a.name, in, got, w[j])
			}
		}
	}
}

// RFC 2202's HMAC-MD5 and HMAC-SHA1 cases 1 and 2, RFC 4231's
// HMAC-SHA224/256/384/512 cases 1 and 2, and HMAC over SHA3-256 and
// BLAKE2b-512 as Python's hmac module computes them.
func TestHMAC_KnownAnswers(t *testing.T) {
	key1 := strings.Repeat("\x0b", 20)
	for _, tc := range []struct {
		name, key, data, want string
	}{
		{"Md5", strings.Repeat("\x0b", 16), "Hi There", "9294727a3638bb1c13f48ef8158bfc9d"},
		{"Md5", "Jefe", "what do ya want for nothing?", "750c783e6ab0b503eaa86e310a5db738"},
		{"Sha1", key1, "Hi There", "b617318655057264e28bc0b6fb378c8ef146be00"},
		{"Sha1", "Jefe", "what do ya want for nothing?", "effcdf6ae5eb2fa2d27416d5f184df9c259a7c79"},
		{"Sha224", key1, "Hi There", "896fb1128abbdf196832107cd49df33f47b4b1169912ba4f53684b22"},
		{"Sha256", key1, "Hi There", "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"},
		{"Sha256", "Jefe", "what do ya want for nothing?", "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"},
		{"Sha384", key1, "Hi There", "afd03944d84895626b0825f4ab46907f15f9dadbe4101ec682aa034c7cebc59cfaea9ea9076ede7f4af152e8b2fa9cb6"},
		{"Sha512", key1, "Hi There", "87aa7cdea5ef619d4ff0b4241a1d6cb02379f4e2ce4ec2787ad0b30545e17cdedaa833b7d6b8a702038b274eaea3f4e4be9d914eeb61f1702e696c203a126854"},
		{"Sha512", "Jefe", "what do ya want for nothing?", "164b7a7bfcf819e2e395fbe73b56e0a387bd64222e831fd610270cd7ea2505549758bf75c05a994a6d034f65f8f0e6fdcaeab1a34d4a6b4b636e070a38bce737"},
		{"Sha3With256", "key", "The quick brown fox jumps over the lazy dog", "8c6e0683409427f8931711b10ca92a506eb1fafa48fadd66d76126f47ac2c333"},
		{"Blake2b512", "key", "abc", "05cc4815438d5cfe68fff446b8df57828cc96189de4b4e928e3f06d815d64e5bc15124a02ffd39859b3e2476da03bc0235ca86df623af2a5631779809e9fd04a"},
	} {
		got := hex.EncodeToString([]byte(HMAC(codeOf(t, tc.name), rt.Bytes(tc.key), rt.Bytes(tc.data))))
		if got != tc.want {
			t.Errorf("HMAC-%s(%q, %q) = %s, want %s", tc.name, tc.key, tc.data, got, tc.want)
		}
	}
}

// FIPS 202's SHAKE examples over the empty input, and a longer output
// starting with every shorter one.
func TestShake_KnownAnswers(t *testing.T) {
	if got := hex.EncodeToString([]byte(Shake128("", rt.SmallInt(32)))); got != "7f9c2ba4e88f827d616045507605853ed73b8093f6efbc88eb1a6eacfa66ef26" {
		t.Errorf("SHAKE128(\"\", 32) = %s", got)
	}
	if got := hex.EncodeToString([]byte(Shake256("", rt.SmallInt(64)))); got != "46b9dd2b0ba88d13233b3feb743eeb243fcd52ea62b81b82b50c27646ed5762fd75dc4ddd8c0f200cb05019d67b592f6fc821c49479ab48640292eacb3b7c4be" {
		t.Errorf("SHAKE256(\"\", 64) = %s", got)
	}
	long := Shake128("abc", rt.SmallInt(1000))
	if short := Shake128("abc", rt.SmallInt(7)); !strings.HasPrefix(string(long), string(short)) {
		t.Errorf("SHAKE128's 7 bytes are not the start of its 1000")
	}
	if got := Shake256("abc", rt.SmallInt(0)); got != "" {
		t.Errorf("SHAKE256 of length 0 = %q", got)
	}
}

// A length past what memory holds traps naming the function, rather than
// asking Go for the memory.
func TestShake_ALengthPastMemoryTraps(t *testing.T) {
	defer func() {
		e, ok := recover().(*rt.Error)
		if !ok || !strings.Contains(e.Msg, "hash.shake128: 100000000000000000000 bytes are more than memory holds") {
			t.Errorf("recovered %v, want rt's trap naming hash.shake128 and the length", e)
		}
	}()
	big := rt.IntOf(rt.IntFromBig(mustBig("100000000000000000000")))
	Shake128("", big)
	t.Fatal("no trap")
}

// Feeding an input in chunks of any size gives the digest of the whole, for
// every algorithm and for an HMAC over each, at chunk sizes around each
// hash's block size: 0, 1, the block size less one, the block size, and
// larger than several blocks.
func TestHasher_ChunkedIsWhole(t *testing.T) {
	data := make([]byte, 3000)
	for i := range data {
		data[i] = byte(i*7 + i/251)
	}
	key := rt.Bytes("a key")
	for i, a := range algorithms {
		code := int64(i)
		block := a.make().BlockSize()
		for _, size := range []int{0, 1, block - 1, block, 5*block + 3} {
			for _, mac := range []bool{false, true} {
				h, want := HasherNew(code), Digest(code, rt.Bytes(data))
				if mac {
					h, want = HasherNewHMAC(code, key), HMAC(code, key, rt.Bytes(data))
				}
				h = feed(h, data, size)
				if got := HasherFinish(h); got != want {
					t.Errorf("%s (HMAC %v) in chunks of %d: %x, want %x", a.name, mac, size, got, want)
				}
			}
		}
	}
}

// feed is h after it reads data in chunks of size bytes, with an empty
// chunk between each when size is 0.
func feed(h rt.Hasher, data []byte, size int) rt.Hasher {
	if size == 0 {
		h = HasherUpdate(h, "")
		h = HasherUpdate(h, rt.Bytes(data))
		return HasherUpdate(h, "")
	}
	for start := 0; start < len(data); start += size {
		h = HasherUpdate(h, rt.Bytes(data[start:min(start+size, len(data))]))
	}
	return h
}

// A Hasher updated twice gives two digests, each the digest of its own
// input, and finishing does not change it: for every algorithm, plain and
// HMAC, the fork's state is a copy.
func TestHasher_AForkGivesBothDigests(t *testing.T) {
	key := rt.Bytes("k")
	for i, a := range algorithms {
		code := int64(i)
		for _, mac := range []bool{false, true} {
			start, whole := HasherNew(code), func(s string) rt.Bytes { return Digest(code, rt.Bytes(s)) }
			if mac {
				start, whole = HasherNewHMAC(code, key), func(s string) rt.Bytes { return HMAC(code, key, rt.Bytes(s)) }
			}
			head := HasherUpdate(start, "shared prefix ")
			left := HasherUpdate(head, "left")
			right := HasherUpdate(head, "right")
			for _, c := range []struct {
				h    rt.Hasher
				read string
			}{{right, "shared prefix right"}, {left, "shared prefix left"}, {head, "shared prefix "}, {start, ""}, {head, "shared prefix "}} {
				if got, want := HasherFinish(c.h), whole(c.read); got != want {
					t.Errorf("%s (HMAC %v) after %q: %x, want %x", a.name, mac, c.read, got, want)
				}
			}
		}
	}
}

func mustBig(s string) *big.Int {
	z, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic(s)
	}
	return z
}
