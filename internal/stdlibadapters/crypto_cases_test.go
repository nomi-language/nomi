package stdlibadapters

import (
	"encoding/hex"
	"strings"

	"github.com/nomi-language/nomi/internal/stdhash"
	"github.com/nomi-language/nomi/internal/stdhttp"
	"github.com/nomi-language/nomi/rt"
)

// The keys the std::signatures::host cases run over: RFC 8032's first Ed25519
// key, a P-256 key from a fixed scalar, and keys that do not parse. A
// freshly generated key, an ECDSA or PSS signature and an RSA key are
// random, so their rows are recorded by shape.
var (
	sigEdSeed = mustHex("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
	sigEdPriv = string(stdhttp.SigEd25519FromSeed(rt.Bytes(sigEdSeed)).Ok)
	sigEdPub  = string(stdhttp.SigPublicOf(rt.Bytes(sigEdPriv)))
	// RFC 7515, appendix A.3's P-256 key.
	sigP256 = string(stdhttp.SigPrivateFromPEM("-----BEGIN PRIVATE KEY-----\n" +
		"MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgjpsQnnGQmL+YBIff\n" +
		"H1136cspYG6+0iY7X1fCE9+E9LKhRANCAAR/zc4ncPbEXUGDy+5v20t7WAczNXvp\n" +
		"7xO6z248e9FURcfxRM0bvZt+hyzf7bnuufSzaV1uqQskrYpGIyiFiOWt\n" +
		"-----END PRIVATE KEY-----\n").Ok)
	sigP256Pub = string(stdhttp.SigPublicOf(rt.Bytes(sigP256)))
	// A 2048-bit RSA key made with OpenSSL for these cases, and its
	// RSA-OAEP-SHA-256 encryption of "a secret for ada" with the label
	// "nomi".
	sigRSA = string(stdhttp.SigPrivateFromPEM("-----BEGIN PRIVATE KEY-----\n" +
		"MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQDKecooyQEHzzZG\n" +
		"VcX745uYNWDg/vWf/c4kaaQR0J7Y/bmtF4lCM77GXb2gA/OYBdvQlvY7C8BbcRea\n" +
		"sbUvWDFc1FH+G+AMC7ZCwXlRpA86uPCiGbUmAE6zjP0j+h82eXpdMvdXlI4SYNhi\n" +
		"WEayjmcd1j6CT7hPCmmL990JyI6AQGb4/UiaOJo21/diWF4dG2Et+wX+CYqSvPOI\n" +
		"vvvANRzvRbcPe0abYdTrm+Y2Oheif7SdH9ASn5ZPJa/j+9CeEX6O3Q4IBb0xabfT\n" +
		"3ay5gzT9vE+zEw/7Pc6T7A/QIgt3bqb8bwxF0buKS8gTXaotek13/Kr8pGGnQLAT\n" +
		"BwH6E+m9AgMBAAECggEAES8I4zE77ye82XdKGDiW6fINyMqamft2VUzJ6eEkuhG+\n" +
		"glwu//lYRYmLIFt1fv9wSmGlgBldN+V/NVfDosL/XXt/getntl4AF+ZHn5Am4rrz\n" +
		"YS6BWzpHc6GRkk4V/A+PPP2oVDx9Wj3x31bSmTDQu42FPJX7dGnTz1c34S6uOWAX\n" +
		"IOoPSSm3whxDYVEot7iVXShszy+CzzQW1GWu+p1QPy8O0UarhiNrGS4IuPSz/c16\n" +
		"YyHjtPrP7MVNS7CIiuEbh+B/mKzSs5LaBOQjuqzltUihcnreekZwCkB7mH6ySZHA\n" +
		"5SFY5NCzAe1KvX3LNJ3GDkhqOtkxiy17VBKnSVaE2QKBgQDz0pkukOXvUVDlD77s\n" +
		"jFfGVxuDfO1Vi9pIejQVE5gGAXcbhmDhiVFi8VN2J83Hit4LkiXv6DmGtlYKgOLR\n" +
		"oWbwurQeqIcGXJZvd48o/nrjejaggPFtQN0VMuGwXrjICz+9NcFQFhV8xFPQCWQa\n" +
		"K1LMojzcK/gYo7ZFajvIr/coCwKBgQDUloyYjaY1oPJt18aMT/2j9urVdlNBVELF\n" +
		"LXLnZaCMRh47yOtpLP4R2Lk1GMJuWsNAQhpsgbu4iKU02jjthh4na5YK18oK25DW\n" +
		"x1t3LIFZykuoPZ+cyLItPglGc5tleFUYCcY4W9j5Vhw1CMDq8CirRNX4FsCE1NKQ\n" +
		"JUApLjqqVwKBgCgAZSE+pHqe3Ka37prga4YexGTOGiHuz+EUK3dJv6c8lk3tvfdC\n" +
		"kBergAXigeA66DL9xzuU3ds8DuGqQ4wg7sKP3v0oqP5iMkwXUWVoWsD3S/8JKJLE\n" +
		"EYFo154X7Djln49rQkvhU6UkkJuQ7ttcQDlSZKm6jPl94mmBBE3okKQBAoGBAMkU\n" +
		"QZZGXVFalBRLxZSQUMRXWFZiXmtrrOBeldHDsbEc3ZoE+I56LTNqP/y2F6pUFVzh\n" +
		"aab6qznnngQ46SHZ+wmkXL6Sz5unhP9yf9Rgd1Ub4KYY67FbsBTVGebWkMTQpgRf\n" +
		"sms6jSeCSJntVrSiRoWBTbYkpyK9PxFy+pY5EOpvAoGAfUp2r2aR0Rw9Ae9/iW9i\n" +
		"eWmzYFz3pNIfqz1IqQ7PPnv88Wctrm/lgQ/5D+YZZBT98AjVHAsjz79NeQtXzlec\n" +
		"5eOVa2wo78qS6+xPLUB+GwmTVM7U0U/YO+B2y5FJNkXFO9DUahlAsG/TYJWQUf58\n" +
		"TqgaPhXoq+aPE/3tIG0NYgY=\n" +
		"-----END PRIVATE KEY-----\n").Ok)
	sigRSAPub  = string(stdhttp.SigPublicOf(rt.Bytes(sigRSA)))
	sigRSAOAEP = mustHex("7bb4bddafd2ffb72f9b0b8adf85edce98b3bdf27136c8eaabe71ce51514a8ae35ca2754400e20b37edaa5ff8f3234f66935af97380413eb06c725a095631755a8410e45b131f51c5caaf323fa9990d4b63751e804b55451a322404a0ef0514a210fe8208221b6fe2ba0d85d2cb0df20e27c2eab7389a33b9b0e5b37c5cdd342c23e5bd7f05d5944276fedf657bf86c6dafb14fcd4dfe77d1173085b06bf97176f6f2696983acfcc2eda795cf5adb53ece6de67ca6c698f5410050d76e36c5394c7a7fa67d05d6ea74c2fb2e95394d78601a897b95f463ece3bfe5162d2c9158341f8381685f5ebadf8e7a05f12e6aa1a37fe3f5d7ee78fc3f401c54b033012dc")
)

// RFC 5869, appendix A.1's input keying material, salt and info.
var (
	hkdfSecret = mustHex("0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b")
	hkdfSalt   = mustHex("000102030405060708090a0b0c")
	hkdfInfo   = mustHex("f0f1f2f3f4f5f6f7f8f9")
)

func mustHex(s string) string {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func cryptoCases() map[string][]hostCase {
	edPEM := string(stdhttp.SigPrivateToPEM(rt.Bytes(sigEdPriv)))
	edPubPEM := string(stdhttp.SigPublicToPEM(rt.Bytes(sigEdPub)))
	message := hBytes("hello")
	edSig := string(stdhttp.SigSign(rt.Bytes(sigEdPriv), 0, rt.Bytes("hello")))
	// std/hash's digests: each algorithm's code over a few inputs, and a
	// Hasher of each, plain and HMAC, part way through one.
	blobs := []any{hBytes(""), hBytes("abc"), hBytes("\x00\xff"), hBytes(strings.Repeat("0123456789", 30))}
	var digests, macs, fresh, keyed, updates, hashers []hostCase
	var started []any
	for code := range int64(13) {
		fresh = append(fresh, hostCase{args: args(hInt(code))})
		for _, b := range blobs {
			digests = append(digests, hostCase{args: args(hInt(code), b)})
		}
		for _, k := range []any{hBytes(""), hBytes("key"), hBytes(strings.Repeat("k", 200))} {
			keyed = append(keyed, hostCase{args: args(hInt(code), k)})
			macs = append(macs, hostCase{args: args(hInt(code), k, hBytes("The quick brown fox"))})
		}
		h := stdhash.HasherUpdate(stdhash.HasherNew(code), "ab")
		started = append(started, h, stdhash.HasherUpdate(stdhash.HasherNewHMAC(code, "key"), "ab"))
	}
	for _, h := range started {
		hashers = append(hashers, hostCase{args: args(h)})
		for _, b := range blobs[:3] {
			updates = append(updates, hostCase{args: args(h, b)})
		}
	}
	var shakes []hostCase
	for _, b := range blobs[:3] {
		for _, n := range []any{hInt(0), hInt(1), hInt(32), hInt(200)} {
			shakes = append(shakes, hostCase{args: args(b, n)})
		}
	}
	shakes = append(shakes, hostCase{args: args(hBytes(""), hBig("18446744073709551616"))})
	return map[string][]hostCase{
		"hash.digest_by":       digests,
		"hash.hmac_by":         macs,
		"hash.hasher_new":      fresh,
		"hash.hasher_new_hmac": keyed,
		"hash.Hasher.update":   updates,
		"hash.Hasher.finish":   hashers,
		"hash.shake128":        shakes,
		"hash.shake256":        shakes,
		"hash.pbkdf2_sha256": cases(
			args(hBytes("passwd"), hBytes("salt"), hInt(1), hInt(64)),
			args(hBytes(""), hBytes(""), hInt(2), hInt(16)),
			args(hBytes("p"), hBytes("s"), hInt(1), hInt(33)),
		),
		// RFC 5869, appendix A.1's inputs over SHA-256, SHA-512, SHA3-256
		// and BLAKE2s, the limit's edge, and a length past int64.
		"hash.hkdf_by": cases(
			args(hInt(0), hBytes(hkdfSecret), hBytes(hkdfSalt), hBytes(hkdfInfo), hInt(42)),
			args(hInt(1), hBytes(hkdfSecret), hBytes(hkdfSalt), hBytes(hkdfInfo), hInt(42)),
			args(hInt(7), hBytes(hkdfSecret), hBytes(hkdfSalt), hBytes(hkdfInfo), hInt(42)),
			args(hInt(12), hBytes(hkdfSecret), hBytes(hkdfSalt), hBytes(hkdfInfo), hInt(42)),
			args(hInt(0), hBytes(""), hBytes(""), hBytes(""), hInt(8160)),
			args(hInt(0), hBytes("s"), hBytes(""), hBytes(""), hInt(8161)),
			args(hInt(1), hBytes("s"), hBytes(""), hBytes(""), hInt(16321)),
			args(hInt(0), hBytes("s"), hBytes(""), hBytes(""), hBig("100000000000000000000000")),
		),
		"hash.hkdf_extract_by": cases(
			args(hInt(0), hBytes(hkdfSecret), hBytes(hkdfSalt)),
			args(hInt(1), hBytes(hkdfSecret), hBytes(hkdfSalt)),
			args(hInt(2), hBytes(""), hBytes("")),
			args(hInt(11), hBytes(""), hBytes("")),
		),
		"hash.hkdf_expand_by": cases(
			args(hInt(0), hBytes(mustHex("077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5")), hBytes(hkdfInfo), hInt(42)),
			args(hInt(1), hBytes(strings.Repeat("k", 64)), hBytes(hkdfInfo), hInt(100)),
			args(hInt(0), hBytes("k"), hBytes(""), hInt(8161)),
			args(hInt(1), hBytes("k"), hBytes(""), hInt(16321)),
		),
		"signatures/host.generate_ed25519": {{args: args(), shapeOnly: true}},
		"signatures/host.generate_ecdsa": {
			{args: args(hInt(1)), shapeOnly: true},
			{args: args(hInt(2)), shapeOnly: true},
			{args: args(hInt(3)), shapeOnly: true},
		},
		"signatures/host.scheme_name": unary(hInt(0), hInt(1), hInt(6), hInt(12)),
		"signatures/host.scheme_kind": unary(hInt(0), hInt(1), hInt(2), hInt(3), hInt(4), hInt(5), hInt(6), hInt(7), hInt(8), hInt(9), hInt(10), hInt(11), hInt(12)),
		"signatures/host.generate_rsa": {
			{args: args(hInt(1024))},
			{args: args(hInt(8193))},
			{args: args(hInt(2048)), shapeOnly: true},
		},
		"signatures/host.private_from_der": unary(hBytes(sigEdPriv), hBytes(sigP256), hBytes("not a key"), hBytes("")),
		"signatures/host.private_from_pem": unary(hStr(edPEM), hStr(edPubPEM), hStr("hello"),
			hStr("-----BEGIN ENCRYPTED PRIVATE KEY-----\nAAAA\n-----END ENCRYPTED PRIVATE KEY-----\n"),
			hStr("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n")),
		"signatures/host.private_to_pem":    unary(hBytes(sigEdPriv), hBytes(sigP256)),
		"signatures/host.private_kind":      unary(hBytes(sigEdPriv), hBytes(sigP256), hBytes("junk")),
		"signatures/host.public_of":         unary(hBytes(sigEdPriv), hBytes(sigP256), hBytes("junk")),
		"signatures/host.public_from_der":   unary(hBytes(sigEdPub), hBytes(sigP256Pub), hBytes(sigEdPriv), hBytes("")),
		"signatures/host.public_from_pem":   unary(hStr(edPubPEM), hStr(edPEM), hStr("")),
		"signatures/host.public_to_pem":     unary(hBytes(sigEdPub), hBytes(sigP256Pub)),
		"signatures/host.public_kind":       unary(hBytes(sigEdPub), hBytes(sigP256Pub), hBytes("junk")),
		"signatures/host.ed25519_from_seed": unary(hBytes(sigEdSeed), hBytes(sigEdSeed[:31]), hBytes("")),
		"signatures/host.ed25519_seed":      unary(hBytes(sigEdPriv), hBytes(sigP256)),
		"signatures/host.ed25519_public_from_bytes": unary(
			hBytes(mustHex("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")), hBytes("short")),
		"signatures/host.ed25519_public_bytes": unary(hBytes(sigEdPub), hBytes(sigP256Pub)),
		"signatures/host.sign": {
			{args: args(hBytes(sigEdPriv), hInt(0), message)},
			{args: args(hBytes(sigEdPriv), hInt(1), message)},
			{args: args(hBytes(sigP256), hInt(1), message), shapeOnly: true},
			{args: args(hBytes(sigP256), hInt(2), message), shapeOnly: true},
			{args: args(hBytes(sigP256), hInt(3), message)},
			{args: args(hBytes(sigEdPriv), hInt(10), message)},
			{args: args(hBytes("junk"), hInt(0), message)},
		},
		"signatures/host.encrypt": {
			{args: args(hBytes(sigRSAPub), hInt(0), message, hBytes("")), shapeOnly: true},
			{args: args(hBytes(sigRSAPub), hInt(2), hBytes(strings.Repeat("x", 127)), hBytes(""))},
		},
		"signatures/host.decrypt": cases(
			args(hBytes(sigRSA), hInt(0), hBytes(sigRSAOAEP), hBytes("nomi")),
			args(hBytes(sigRSA), hInt(0), hBytes(sigRSAOAEP), hBytes("")),
			args(hBytes(sigRSA), hInt(1), hBytes(sigRSAOAEP), hBytes("nomi")),
			args(hBytes(sigRSA), hInt(0), hBytes("short"), hBytes("nomi")),
		),
		"signatures/host.is_valid": cases(
			args(hBytes(sigEdPub), hInt(0), message, hBytes(edSig)),
			args(hBytes(sigEdPub), hInt(0), hBytes("hellO"), hBytes(edSig)),
			args(hBytes(sigEdPub), hInt(1), message, hBytes(edSig)),
			args(hBytes(sigP256Pub), hInt(1), message, hBytes(strings.Repeat("\x00", 64))),
			args(hBytes(sigP256Pub), hInt(2), message, hBytes("")),
			args(hBytes(sigP256Pub), hInt(3), message, hBytes(strings.Repeat("\x00", 96))),
			args(hBytes("junk"), hInt(0), message, hBytes(edSig)),
		),
	}
}
