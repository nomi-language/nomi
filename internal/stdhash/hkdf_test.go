package stdhash

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

func mustUnhex(t *testing.T, s string) rt.Bytes {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return rt.Bytes(b)
}

// hkdfCase is one HKDF known answer: the pseudorandom key extract makes
// and the output key, for an algorithm named as std/hash's Algorithm.
type hkdfCase struct {
	name, algorithm              string
	secret, salt, info, prk, okm string
}

// RFC 5869, appendix A.1 to A.3 (SHA-256) and A.4 (SHA-1), and HKDF over
// SHA-384 and SHA3-256 as Python's hmac and hashlib compose it (RFC 5869's
// two steps written out over hmac.new), on A.1's inputs.
var hkdfCases = []hkdfCase{
	{"A.1", "Sha256", "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b", "000102030405060708090a0b0c", "f0f1f2f3f4f5f6f7f8f9",
		"077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5",
		"3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865"},
	{"A.2", "Sha256",
		"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f" +
			"303132333435363738393a3b3c3d3e3f404142434445464748494a4b4c4d4e4f",
		"606162636465666768696a6b6c6d6e6f707172737475767778797a7b7c7d7e7f808182838485868788898a8b8c8d8e8f" +
			"909192939495969798999a9b9c9d9e9fa0a1a2a3a4a5a6a7a8a9aaabacadaeaf",
		"b0b1b2b3b4b5b6b7b8b9babbbcbdbebfc0c1c2c3c4c5c6c7c8c9cacbcccdcecfd0d1d2d3d4d5d6d7d8d9dadbdcdddedf" +
			"e0e1e2e3e4e5e6e7e8e9eaebecedeeeff0f1f2f3f4f5f6f7f8f9fafbfcfdfeff",
		"06a6b88c5853361a06104c9ceb35b45cef760014904671014a193f40c15fc244",
		"b11e398dc80327a1c8e7f78c596a49344f012eda2d4efad8a050cc4c19afa97c59045a99cac7827271cb41c65e590e09" +
			"da3275600c2f09b8367793a9aca3db71cc30c58179ec3e87c14c01d5c1f3434f1d87"},
	{"A.3", "Sha256", "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b", "", "",
		"19ef24a32c717b167f33a91d6f648bdf96596776afdb6377ac434c1c293ccb04",
		"8da4e775a563c18f715f802a063c5a31b8a11f5c5ee1879ec3454e5f3c738d2d9d201395faa4b61a96c8"},
	{"A.4", "Sha1", "0b0b0b0b0b0b0b0b0b0b0b", "000102030405060708090a0b0c", "f0f1f2f3f4f5f6f7f8f9",
		"9b6c18c432a7bf8f0e71c8eb88f4b30baa2ba243",
		"085a01ea1b10f36933068b56efa5ad81a4f14b822f5b091568a9cdd4f155fda2c22e422478d305f3f896"},
	{"SHA-384", "Sha384", "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b", "000102030405060708090a0b0c", "f0f1f2f3f4f5f6f7f8f9",
		"704b39990779ce1dc548052c7dc39f303570dd13fb39f7acc564680bef80e8dec70ee9a7e1f3e293ef68eceb072a5ade",
		"9b5097a86038b805309076a44b3a9f38063e25b516dcbf369f394cfab43685f748b6457763e4f0204fc5d95d1da3e62587b2" +
			"2eb8943d0fab6bb631a2fe9df1a68c6ce5d56116a52005b3f122b88b39b7251fcd6c44d3ef25f20ed96802bf1b2c1d98bf74"},
	{"SHA3-256", "Sha3With256", "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b", "000102030405060708090a0b0c", "f0f1f2f3f4f5f6f7f8f9",
		"7d4194836f7a113a44677abc825640ade07af1c1d69a9a4b109b280a8fe54ef0",
		"0c5160501d65021deaf2c14f5abce04c5bd2635abceeba61c2edb6e8ed72674900557728f2c9f2c4c179d6076659d9e57811" +
			"332b92a07bf6612275f5a82f00a274819484d003a470a285f9f25d28b6746054b92699ed04fd7740a78e8a38bafa70ec81ef"},
}

// Each case's pseudorandom key is the extract, its output key is the HKDF,
// and the expand of the extract is the HKDF too.
func TestHKDF_KnownAnswers(t *testing.T) {
	for _, c := range hkdfCases {
		code := codeOf(t, c.algorithm)
		secret, salt, info := mustUnhex(t, c.secret), mustUnhex(t, c.salt), mustUnhex(t, c.info)
		length := rt.SmallInt(int64(len(c.okm) / 2))
		if got := hex.EncodeToString([]byte(HKDFExtract(code, secret, salt))); got != c.prk {
			t.Errorf("%s: extract = %s, want %s", c.name, got, c.prk)
		}
		if got := HKDF(code, secret, salt, info, length); got.Tag != rt.TagOk || hex.EncodeToString([]byte(got.Ok)) != c.okm {
			t.Errorf("%s: hkdf = %x %q, want %s", c.name, got.Ok, got.Err, c.okm)
		}
		if got := HKDFExpand(code, mustUnhex(t, c.prk), info, length); got.Tag != rt.TagOk || hex.EncodeToString([]byte(got.Ok)) != c.okm {
			t.Errorf("%s: expand = %x %q, want %s", c.name, got.Ok, got.Err, c.okm)
		}
	}
}

// HKDF over SHA-512 is RFC 5869 with HMAC-SHA512: the pseudorandom key is
// HMAC(salt, secret), and output block i is HMAC(prk, T(i-1) | info | i),
// computed here with crypto/hmac alone over two blocks.
func TestHKDF_Sha512IsRFC5869OverHMACSHA512(t *testing.T) {
	code := codeOf(t, "Sha512")
	secret, salt, info := rt.Bytes("a shared secret"), rt.Bytes("some salt"), rt.Bytes("session key")
	mac := func(key, data []byte) []byte {
		m := hmac.New(sha512.New, key)
		m.Write(data)
		return m.Sum(nil)
	}
	prk := mac([]byte(salt), []byte(secret))
	t1 := mac(prk, append([]byte(info), 1))
	t2 := mac(prk, append(append(append([]byte{}, t1...), info...), 2))
	want := hex.EncodeToString(append(t1, t2[:36]...))

	if got := hex.EncodeToString([]byte(HKDFExtract(code, secret, salt))); got != hex.EncodeToString(prk) {
		t.Errorf("extract = %s, want %x", got, prk)
	}
	if got := HKDF(code, secret, salt, info, rt.SmallInt(100)); got.Tag != rt.TagOk || hex.EncodeToString([]byte(got.Ok)) != want {
		t.Errorf("hkdf = %x %q, want %s", got.Ok, got.Err, want)
	}
	if got := HKDFExpand(code, rt.Bytes(prk), info, rt.SmallInt(100)); got.Tag != rt.TagOk || hex.EncodeToString([]byte(got.Ok)) != want {
		t.Errorf("expand = %x %q, want %s", got.Ok, got.Err, want)
	}
}

// Every algorithm in the table derives a key, as long as asked, whose
// extract is as long as its digest: HKDF reaches every algorithm HMAC does.
func TestHKDF_EveryAlgorithm(t *testing.T) {
	for i, a := range algorithms {
		code := int64(i)
		size := len(Digest(code, ""))
		if got := HKDFExtract(code, "secret", "salt"); len(got) != size {
			t.Errorf("%s: extract is %d bytes, want %d", a.name, len(got), size)
		}
		if got := HKDF(code, "secret", "salt", "info", rt.SmallInt(77)); got.Tag != rt.TagOk || len(got.Ok) != 77 {
			t.Errorf("%s: hkdf = %d bytes %q, want 77", a.name, len(got.Ok), got.Err)
		}
	}
}

// A length past 255 blocks is an Err that names the algorithm and the
// limit, at the limit's edge and for a length past int64.
func TestHKDF_RefusesALengthPastItsLimit(t *testing.T) {
	huge, ok := rt.ParseIntValue("100000000000000000000000")
	if !ok {
		t.Fatal("not an Int")
	}
	sha256, sha512, sha384 := codeOf(t, "Sha256"), codeOf(t, "Sha512"), codeOf(t, "Sha384")
	for _, c := range []struct {
		got  rt.Result[rt.Bytes, string]
		want string
	}{
		{HKDF(sha256, "s", "", "", rt.SmallInt(8161)), "an HKDF key over Sha256 is at most 8160 bytes, got 8161"},
		{HKDFExpand(sha512, "s", "", rt.SmallInt(16321)), "an HKDF key over Sha512 is at most 16320 bytes, got 16321"},
		{HKDF(sha384, "s", "", "", rt.SmallInt(12241)), "an HKDF key over Sha384 is at most 12240 bytes, got 12241"},
		{HKDF(sha512, "s", "", "", rt.IntOf(huge)), "an HKDF key over Sha512 is at most 16320 bytes, got 100000000000000000000000"},
	} {
		if c.got.Tag != rt.TagErr || c.got.Err != c.want {
			t.Errorf("got %v, want Err(%q)", c.got, c.want)
		}
	}
	if got := HKDF(sha256, "s", "", "", rt.SmallInt(8160)); got.Tag != rt.TagOk || len(got.Ok) != 8160 {
		t.Errorf("a key of 8160 bytes: %v", got.Err)
	}
}
